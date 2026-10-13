//go:build live

package live

import (
	"context"
	"crypto/rand"
	"errors"
	"regexp"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/dls"
)

var (
	nsFail      = env("DLS_NS_FAIL", "cuenta")
	nsQueue     = env("DLS_NS_QUEUE", "cuenta_cola")
	nsAlert     = env("DLS_NS_ALERT", "cuenta_alerta")
	nsReadWrite = env("DLS_NS_READ_WRITE", "archivo")
)

func idem(options *dls.AcquireLockOptions) *dls.AcquireLockOptions {
	if options == nil {
		options = &dls.AcquireLockOptions{}
	}
	options.IdempotencyKey = rand.Text()
	return options
}

func begin(t *testing.T, client *dls.Client, timeout time.Duration) string {
	t.Helper()
	return must(client.BeginTransaction(ctx, &dls.BeginTransactionOptions{Timeout: timeout})).TransactionID
}

func releaseQuietly(client *dls.Client, transactions ...string) {
	for _, transactionID := range transactions {
		_ = client.ReleaseTransactionLocks(ctx, transactionID)
	}
}

func crossing(client *dls.Client, namespace, first, second string, pause, wait, lifetime time.Duration) <-chan error {
	result := make(chan error, 1)
	go func() {
		result <- client.WithTransaction(ctx, func(ctx context.Context, tx dls.Tx) error {
			if _, err := tx.AcquireLock(ctx, namespace, first, dls.Exclusive, idem(nil)); err != nil {
				return err
			}
			time.Sleep(pause)
			_, err := tx.AcquireLock(ctx, namespace, second, dls.Exclusive, idem(&dls.AcquireLockOptions{Timeout: wait}))
			return err
		}, &dls.TransactionOptions{Timeout: lifetime})
	}()
	return result
}

func reasonOf(err error) string {
	var base *caerus.Error
	if errors.As(err, &base) {
		return base.Reason()
	}
	return ""
}

func TestSharedReadAndLockStatus(t *testing.T) {
	client := dlsClient(t, 0)
	file := key("archivo")
	tx1, tx2, tx3 := begin(t, client, 20*time.Second), begin(t, client, 20*time.Second), begin(t, client, 20*time.Second)
	defer releaseQuietly(client, tx1, tx2, tx3)
	l1 := must(client.AcquireLock(ctx, nsReadWrite, file, tx1, dls.SharedRead, idem(nil)))
	l2 := must(client.AcquireLock(ctx, nsReadWrite, file, tx2, dls.SharedRead, idem(nil)))
	report(t, "dos lectores entran a la vez", string(l1.Status)+" "+string(l2.Status))
	check(t, l1.Status == dls.Acquired && l2.Status == dls.Acquired, "the readers did not get in")
	status := must(client.GetLockStatus(ctx, nsReadWrite, file))
	report(t, "el estado dice SHARED_READ y lista a los dos", []any{status.CurrentMode, len(status.ActiveHolders)})
	check(t, status.CurrentMode == dls.SharedRead, "mode %q", status.CurrentMode)
	check(t, len(status.ActiveHolders) == 2, "holders %d", len(status.ActiveHolders))
	for _, holder := range status.ActiveHolders {
		check(t, holder.FencingToken > 0, "a reader without fencing token")
	}
	_, writer := client.AcquireLock(ctx, nsReadWrite, file, tx3, dls.Exclusive, idem(&dls.AcquireLockOptions{Timeout: 4 * time.Second}))
	report(t, "el escritor no entra con lectores activos", writer)
	var base *caerus.Error
	check(t, errors.As(writer, &base), "the writer got in")
	ok(t, client.ReleaseLock(ctx, l1.LockID, tx1))
	after := must(client.GetLockStatus(ctx, nsReadWrite, file))
	report(t, "soltar un lector deja al otro", len(after.ActiveHolders))
	check(t, len(after.ActiveHolders) == 1, "holders %d", len(after.ActiveHolders))
}

func TestDeniedLockInAFailNamespace(t *testing.T) {
	client := dlsClient(t, 0)
	account := key("cuenta")
	err := client.WithTransaction(ctx, func(ctx context.Context, tx1 dls.Tx) error {
		own, err := tx1.AcquireLock(ctx, nsFail, account, dls.Exclusive, idem(nil))
		if err != nil {
			return err
		}
		report(t, "el primero lo toma, con fencing token", []any{own.Status, own.FencingToken})
		check(t, own.Status == dls.Acquired && own.FencingToken > 0, "bad first lock %+v", own)
		return client.WithTransaction(ctx, func(ctx context.Context, tx2 dls.Tx) error {
			_, err := tx2.AcquireLock(ctx, nsFail, account, dls.Exclusive, idem(&dls.AcquireLockOptions{Timeout: 5 * time.Second}))
			report(t, "el segundo recibe LockDeniedError con LOCK_DENIED", err)
			var denied *dls.LockDeniedError
			check(t, errors.As(err, &denied), "expected LockDeniedError, got %v", err)
			check(t, denied.Reason() == "LOCK_DENIED", "reason %q", denied.Reason())
			return nil
		}, &dls.TransactionOptions{Timeout: 8 * time.Second})
	}, &dls.TransactionOptions{Timeout: 15 * time.Second})
	ok(t, err)
}

func TestTwoWayDeadlockWithKillPriority(t *testing.T) {
	client := dlsClient(t, 0)
	x, y := key("X"), key("Y")
	started := time.Now()
	first := crossing(client, nsQueue, x, y, 1500*time.Millisecond, 30*time.Second, 30*time.Second)
	time.Sleep(700 * time.Millisecond)
	second := crossing(client, nsQueue, y, x, 300*time.Millisecond, 30*time.Second, 30*time.Second)
	firstErr, secondErr := <-first, <-second
	elapsed := time.Since(started)
	var victims []error
	for _, err := range []error{firstErr, secondErr} {
		if err != nil {
			victims = append(victims, err)
		}
	}
	report(t, "una victima y un ganador", []any{len(victims), elapsed.Round(100 * time.Millisecond)})
	check(t, len(victims) == 1, "victims %v", victims)
	var deadlock *dls.DeadlockAbortedError
	check(t, errors.As(victims[0], &deadlock), "expected DeadlockAbortedError, got %v", victims[0])
	check(t, deadlock.Reason() == "DEADLOCK_DETECTED", "reason %q", deadlock.Reason())
	check(t, deadlock.RequestID() != "", "missing request id")
	check(t, secondErr != nil, "the younger transaction should be the victim")
	check(t, elapsed < 10*time.Second, "took %v", elapsed)
}

func TestTriangularDeadlock(t *testing.T) {
	client := dlsClient(t, 0)
	x, y, z := key("TX"), key("TY"), key("TZ")
	runs := []<-chan error{
		crossing(client, nsQueue, x, y, 400*time.Millisecond, 30*time.Second, 30*time.Second),
		crossing(client, nsQueue, y, z, 400*time.Millisecond, 30*time.Second, 30*time.Second),
		crossing(client, nsQueue, z, x, 400*time.Millisecond, 30*time.Second, 30*time.Second),
	}
	var victims []error
	for _, run := range runs {
		if err := <-run; err != nil {
			victims = append(victims, err)
		}
	}
	report(t, "se rompe el ciclo sin matar a todos", len(victims))
	check(t, len(victims) >= 1 && len(victims) <= 2, "victims %v", victims)
	for _, victim := range victims {
		check(t, reasonOf(victim) == "DEADLOCK_DETECTED", "victim %v", victim)
	}
}

func TestDeadlockWithAlertStrategyAbortsNobody(t *testing.T) {
	client := dlsClient(t, 0)
	x, y := key("LX"), key("LY")
	first := crossing(client, nsAlert, x, y, 300*time.Millisecond, 6*time.Second, 20*time.Second)
	second := crossing(client, nsAlert, y, x, 300*time.Millisecond, 6*time.Second, 20*time.Second)
	results := []error{<-first, <-second}
	aborted := 0
	for _, err := range results {
		if reasonOf(err) == "DEADLOCK_DETECTED" {
			aborted++
		}
	}
	report(t, "nadie fue abortado por deadlock", []any{aborted, results})
	check(t, aborted == 0, "aborted %d", aborted)
	for _, err := range results {
		if err != nil {
			var timeout *dls.TimeoutError
			check(t, errors.As(err, &timeout), "it can only end by time, got %v", err)
		}
	}
}

func TestTransactionStatusWithALockInTheQueueAndOnQueued(t *testing.T) {
	client := dlsClient(t, 0)
	lockKey := key("cola")
	tx1, tx2 := begin(t, client, 20*time.Second), begin(t, client, 20*time.Second)
	defer releaseQuietly(client, tx1, tx2)
	must(client.AcquireLock(ctx, nsQueue, lockKey, tx1, dls.Exclusive, idem(nil)))
	var notices atomic.Int32
	var mu sync.Mutex
	var order []string
	granted := make(chan *dls.LockHolder, 1)
	failed := make(chan error, 1)
	go func() {
		lock, err := client.AcquireLock(ctx, nsQueue, lockKey, tx2, dls.Exclusive, idem(&dls.AcquireLockOptions{
			Timeout: 8 * time.Second,
			OnQueued: func() {
				notices.Add(1)
				mu.Lock()
				order = append(order, "en cola")
				mu.Unlock()
			},
		}))
		if err != nil {
			failed <- err
			return
		}
		mu.Lock()
		order = append(order, "concedido")
		mu.Unlock()
		granted <- lock
	}()
	time.Sleep(1500 * time.Millisecond)
	holding := must(client.GetTransactionStatus(ctx, tx1))
	queued := must(client.GetTransactionStatus(ctx, tx2))
	report(t, "las dos transacciones informan su estado", holding.Status+" / "+queued.Status)
	check(t, holding.Status != "" && queued.Status != "", "empty status")
	ok(t, client.ReleaseTransactionLocks(ctx, tx1))
	select {
	case lock := <-granted:
		mu.Lock()
		defer mu.Unlock()
		report(t, "onQueued avisa una vez y antes de conceder", []any{notices.Load(), order})
		check(t, lock.Status == dls.Acquired, "status %q", lock.Status)
		check(t, notices.Load() == 1, "notices %d", notices.Load())
		check(t, slices.Equal(order, []string{"en cola", "concedido"}), "order %v", order)
	case err := <-failed:
		t.Fatalf("the queued lock failed: %v", err)
	}
}

func TestAQueuedLockWithoutItsOwnTimeoutUsesTheClientDeadline(t *testing.T) {
	client := dlsClient(t, 0)
	impatient := dlsClient(t, 2*time.Second)
	lockKey := key("plazo")
	tx1, tx2 := begin(t, client, 30*time.Second), begin(t, client, 30*time.Second)
	defer releaseQuietly(client, tx1, tx2)
	must(client.AcquireLock(ctx, nsQueue, lockKey, tx1, dls.Exclusive, idem(nil)))
	started := time.Now()
	_, err := impatient.AcquireLock(ctx, nsQueue, lockKey, tx2, dls.Exclusive, idem(nil))
	elapsed := time.Since(started)
	report(t, "sin timeout propio corta con el del cliente", []any{err, elapsed.Round(100 * time.Millisecond)})
	var timeout *dls.TimeoutError
	check(t, errors.As(err, &timeout), "expected TimeoutError, got %v", err)
	check(t, elapsed > 1500*time.Millisecond && elapsed < 5*time.Second, "took %v", elapsed)
}

func TestAClosedTransactionCannotTakeLocks(t *testing.T) {
	client := dlsClient(t, 0)
	transactionID := begin(t, client, 20*time.Second)
	ok(t, client.ReleaseTransactionLocks(ctx, transactionID))
	_, err := client.AcquireLock(ctx, nsQueue, key("cerrada"), transactionID, dls.Exclusive, idem(nil))
	report(t, "una transaccion cerrada no toma locks", err)
	var notActive *dls.TransactionNotActiveError
	check(t, errors.As(err, &notActive), "expected TransactionNotActiveError, got %v", err)
	check(t, notActive.Reason() == "TRANSACTION_NOT_ACTIVE", "reason %q", notActive.Reason())
}

func TestWithTransactionRenewsALongJob(t *testing.T) {
	client := dlsClient(t, 0)
	lockKey := key("largo")
	var before, after int64
	var status string
	err := client.WithTransaction(ctx, func(ctx context.Context, tx dls.Tx) error {
		if _, err := tx.AcquireLock(ctx, nsQueue, lockKey, dls.Exclusive, idem(nil)); err != nil {
			return err
		}
		before = must(client.GetTransactionStatus(ctx, tx.TransactionID())).ExpiresAt
		time.Sleep(7 * time.Second)
		current := must(client.GetTransactionStatus(ctx, tx.TransactionID()))
		after, status = current.ExpiresAt, current.Status
		return nil
	}, &dls.TransactionOptions{Timeout: 4 * time.Second})
	ok(t, err)
	report(t, "una transaccion de 4 s sigue viva a los 7 s y su vencimiento avanza", []any{status, after - before})
	check(t, status == "ACTIVE", "status %q", status)
	check(t, after-before >= 8000, "expiry moved %d ms", after-before)
}

func TestAWrongAPIKeyIsAnAuthenticationErrorInTheDLS(t *testing.T) {
	apiKey(t)
	stranger, err := dls.NewClient(dls.ClientOptions{APIKey: "caer_dev_no_existe_" + rand.Text(), Endpoint: endpoint(), TLS: tls()})
	ok(t, err)
	defer stranger.Close()
	_, err = stranger.BeginTransaction(ctx, nil)
	report(t, "una API Key que no existe da error de autenticacion", err)
	var auth *dls.AuthenticationError
	check(t, errors.As(err, &auth), "expected AuthenticationError, got %v", err)
	check(t, auth.RequestID() != "", "missing request id")
}

func TestAnEngineErrorCarriesARequestID(t *testing.T) {
	_, err := dlsClient(t, 0).RenewTransaction(ctx, "00000000-0000-4000-8000-"+time.Now().Format("150405000000"), time.Second)
	report(t, "el error trae el requestId", err)
	var base *caerus.Error
	check(t, errors.As(err, &base), "expected a Caerus error, got %v", err)
	check(t, regexp.MustCompile(`^req_[0-9a-f-]{36}$`).MatchString(base.RequestID()), "request id %q", base.RequestID())
}

func TestAnIdempotencyKeyReplaysTheLockAndAnotherKeyIsAlreadyHeld(t *testing.T) {
	client := dlsClient(t, 0)
	lockKey := key("idem")
	err := client.WithTransaction(ctx, func(ctx context.Context, tx dls.Tx) error {
		same := idem(nil)
		first := must(tx.AcquireLock(ctx, nsQueue, lockKey, dls.Exclusive, same))
		again := must(tx.AcquireLock(ctx, nsQueue, lockKey, dls.Exclusive, same))
		_, other := tx.AcquireLock(ctx, nsQueue, lockKey, dls.Exclusive, idem(nil))
		report(t, "la misma clave devuelve el mismo lock y otra clave da LOCK_ALREADY_HELD_EXCLUSIVELY", []any{*first == *again, other})
		check(t, *first == *again, "the replay returned another lock")
		var held *dls.LockAlreadyHeldError
		check(t, errors.As(other, &held), "expected LockAlreadyHeldError, got %v", other)
		check(t, held.Reason() == "LOCK_ALREADY_HELD_EXCLUSIVELY", "reason %q", held.Reason())
		return nil
	}, &dls.TransactionOptions{Timeout: 15 * time.Second})
	ok(t, err)
}

func TestFencingTokensGrowAcrossHandoffs(t *testing.T) {
	client := dlsClient(t, 0)
	lockKey := key("fencing")
	var tokens []int64
	for range 3 {
		ok(t, client.WithTransaction(ctx, func(ctx context.Context, tx dls.Tx) error {
			lock, err := tx.AcquireLock(ctx, nsQueue, lockKey, dls.Exclusive, idem(nil))
			if err == nil {
				tokens = append(tokens, lock.FencingToken)
			}
			return err
		}, &dls.TransactionOptions{Timeout: 10 * time.Second, DisableAutoRenew: true}))
	}
	report(t, "los fencing tokens crecen", tokens)
	check(t, slices.IsSorted(tokens) && tokens[0] < tokens[1] && tokens[1] < tokens[2], "tokens %v", tokens)
}

func TestCancellingTheContextCutsARealWaitInTheQueue(t *testing.T) {
	client := dlsClient(t, 0)
	lockKey := key("corte")
	tx1, tx2 := begin(t, client, 20*time.Second), begin(t, client, 20*time.Second)
	defer releaseQuietly(client, tx1, tx2)
	must(client.AcquireLock(ctx, nsQueue, lockKey, tx1, dls.Exclusive, idem(nil)))
	cancellable, cancel := context.WithCancel(ctx)
	time.AfterFunc(time.Second, cancel)
	started := time.Now()
	_, err := client.AcquireLock(cancellable, nsQueue, lockKey, tx2, dls.Exclusive, idem(&dls.AcquireLockOptions{Timeout: 10 * time.Second}))
	report(t, "cancelar el contexto corta una espera real", []any{err, time.Since(started).Round(100 * time.Millisecond)})
	check(t, errors.Is(err, context.Canceled), "expected the cancellation, got %v", err)
	check(t, time.Since(started) < 3*time.Second, "took %v", time.Since(started))
}
