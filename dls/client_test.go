package dls

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

func TestOpensTheTransactionRunsTheCallbackAndReleasesTheLocksAtTheEnd(t *testing.T) {
	engine, calls := scripted(t)
	var result string
	err := connect(t, engine).WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		lock, err := tx.AcquireLock(ctx, "ns", "k", Exclusive, nil)
		if err != nil {
			return err
		}
		result = fmt.Sprintf("%s:%s:%d", tx.TransactionID(), lock.Status, lock.FencingToken)
		return nil
	}, noRenew)
	te.NoError(t, err)
	te.Equal(t, result, "tx-1:ACQUIRED:7")
	te.Equal(t, calls.list(), []string{"begin", "releaseAll"})
	te.Equal(t, engine.LastRequest().(*dlspb.ReleaseTransactionLocksRequest).GetTransactionId(), "tx-1")
}

func TestReleasesTheLocksEvenWhenTheCallbackFailsAndReturnsItsErrorUnwrapped(t *testing.T) {
	engine, calls := scripted(t)
	broken := errors.New("se rompio el trabajo")
	err := connect(t, engine).WithTransaction(ctx, func(context.Context, Tx) error { return broken }, noRenew)
	te.True(t, err == broken, "got %v", err)
	te.Equal(t, calls.list(), []string{"begin", "releaseAll"})
}

func TestReleasesTheLocksEvenWhenTheCallbackPanics(t *testing.T) {
	engine, calls := scripted(t)
	client := connect(t, engine)
	func() {
		defer func() { te.Equal(t, recover(), any("boom")) }()
		_ = client.WithTransaction(ctx, func(context.Context, Tx) error { panic("boom") }, noRenew)
	}()
	te.Equal(t, calls.list(), []string{"begin", "releaseAll"})
}

func TestADeniedLockInsideTheTransactionCutsItAndStillReleases(t *testing.T) {
	engine, calls := scripted(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		return stream.Send(te.WithStatus(dlspb.LockStatus_DENIED))
	})
	err := connect(t, engine).WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		_, err := tx.AcquireLock(ctx, "ns", "k", Exclusive, nil)
		return err
	}, noRenew)
	te.As[*LockDeniedError](t, err)
	te.Equal(t, calls.list(), []string{"begin", "releaseAll"})
}

func TestAFailedReleaseAtTheEndIsSwallowed(t *testing.T) {
	engine, _ := scripted(t)
	te.OnDLS(engine, "releaseTransactionLocks", func(context.Context, *dlspb.ReleaseTransactionLocksRequest) (*emptypb.Empty, error) {
		return nil, te.Status(codes.Unavailable, "se cayo")
	})
	te.NoError(t, connect(t, engine).WithTransaction(ctx, func(context.Context, Tx) error { return nil }, noRenew))
}

func TestReleasesEvenWhenTheCallerContextWasCancelled(t *testing.T) {
	engine, calls := scripted(t)
	cancellable, cancel := context.WithCancel(ctx)
	err := connect(t, engine).WithTransaction(cancellable, func(context.Context, Tx) error {
		cancel()
		return nil
	}, noRenew)
	te.NoError(t, err)
	te.Equal(t, calls.list(), []string{"begin", "releaseAll"})
}

func TestRenewsOnItsOwnWhileTheWorkLasts(t *testing.T) {
	engine, calls := scripted(t)
	te.NoError(t, connect(t, engine).WithTransaction(ctx, func(context.Context, Tx) error {
		time.Sleep(2300 * time.Millisecond)
		return nil
	}, life(2*time.Second)))
	te.True(t, calls.count("renew") >= 2, "renewed %d times", calls.count("renew"))
	te.Equal(t, calls.count("releaseAll"), 1)
}

func TestRenewsByTheIntervalSoTheSafetyWindowStaysTheLifetime(t *testing.T) {
	engine, _ := scripted(t)
	var seen atomic.Pointer[dlspb.RenewTransactionRequest]
	te.OnDLS(engine, "renewTransaction", func(_ context.Context, request *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
		seen.Store(request)
		return &dlspb.RenewTransactionResponse{TransactionId: "tx-1"}, nil
	})
	te.NoError(t, connect(t, engine).WithTransaction(ctx, func(context.Context, Tx) error {
		time.Sleep(1300 * time.Millisecond)
		return nil
	}, life(2*time.Second)))
	te.True(t, seen.Load() != nil, "no renewal")
	te.Equal(t, seen.Load().GetTransactionId(), "tx-1")
	te.Equal(t, seen.Load().GetExtraMs(), int64(1000))
}

func TestWithAutoRenewOffItSendsNotASingleHeartbeat(t *testing.T) {
	engine, calls := scripted(t)
	te.NoError(t, connect(t, engine).WithTransaction(ctx, func(context.Context, Tx) error {
		time.Sleep(1600 * time.Millisecond)
		return nil
	}, &TransactionOptions{Timeout: 2 * time.Second, DisableAutoRenew: true}))
	te.Equal(t, calls.count("renew"), 0)
}

func TestStopsRenewingOnceTheCallbackEnds(t *testing.T) {
	engine, calls := scripted(t)
	te.NoError(t, connect(t, engine).WithTransaction(ctx, func(context.Context, Tx) error {
		time.Sleep(1200 * time.Millisecond)
		return nil
	}, life(2*time.Second)))
	atTheEnd := calls.count("renew")
	time.Sleep(1500 * time.Millisecond)
	te.Equal(t, calls.count("renew"), atTheEnd)
}

func TestIfTheRenewalFailsBeyondRepairTheTransactionIsLost(t *testing.T) {
	engine, calls := scripted(t)
	te.OnDLS(engine, "renewTransaction", func(context.Context, *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
		calls.add("renew")
		return nil, te.Status(codes.NotFound, "la transaccion ya no existe")
	})
	var notified atomic.Value
	var cancelled atomic.Bool
	err := connect(t, engine).WithTransaction(ctx, func(ctx context.Context, _ Tx) error {
		time.Sleep(1600 * time.Millisecond)
		cancelled.Store(ctx.Err() != nil)
		te.As[*NotFoundError](t, context.Cause(ctx))
		return nil
	}, &TransactionOptions{Timeout: 2 * time.Second, OnTransactionLost: func(err error) { notified.Store(err) }})
	te.As[*NotFoundError](t, err)
	te.True(t, notified.Load() == err, "OnTransactionLost got a different error")
	te.True(t, cancelled.Load(), "the callback context should be cancelled")
	te.Equal(t, calls.count("releaseAll"), 1)
}

func TestTwoFailuresInARowOfAnyKindAlsoLoseIt(t *testing.T) {
	engine, calls := scripted(t)
	te.OnDLS(engine, "renewTransaction", func(context.Context, *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
		calls.add("renew")
		return nil, te.Status(codes.Unavailable, "se corto")
	})
	err := connect(t, engine).WithTransaction(ctx, func(context.Context, Tx) error {
		time.Sleep(2600 * time.Millisecond)
		return nil
	}, life(2*time.Second))
	dlsErr := te.As[*Error](t, err)
	te.Equal(t, dlsErr.Code(), caerus.CodeUnknown)
	te.Equal(t, calls.count("renew"), 2)
}

func TestOneFailureFollowedByASuccessIsForgiven(t *testing.T) {
	engine, calls := scripted(t)
	var failedOnce atomic.Bool
	te.OnDLS(engine, "renewTransaction", func(context.Context, *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
		calls.add("renew")
		if failedOnce.CompareAndSwap(false, true) {
			return nil, te.Status(codes.Unavailable, "se corto")
		}
		return &dlspb.RenewTransactionResponse{TransactionId: "tx-1"}, nil
	})
	te.NoError(t, connect(t, engine).WithTransaction(ctx, func(context.Context, Tx) error {
		time.Sleep(3300 * time.Millisecond)
		return nil
	}, life(2*time.Second)))
	te.True(t, calls.count("renew") >= 3, "renewed %d times", calls.count("renew"))
}

func TestOnceLostItWillNotTakeMoreLocks(t *testing.T) {
	engine, _ := scripted(t)
	te.OnDLS(engine, "renewTransaction", func(context.Context, *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
		return nil, te.Status(codes.NotFound, "la transaccion ya no existe")
	})
	var onTake error
	err := connect(t, engine).WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		time.Sleep(1600 * time.Millisecond)
		_, onTake = tx.AcquireLock(ctx, "ns", "k", Exclusive, nil)
		return nil
	}, life(2*time.Second))
	te.As[*NotFoundError](t, err)
	te.As[*NotFoundError](t, onTake)
}

func TestLosingTheTransactionCutsALockThatIsStillWaiting(t *testing.T) {
	engine, _ := scripted(t)
	te.OnDLS(engine, "renewTransaction", func(context.Context, *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
		return nil, te.WithReason(codes.FailedPrecondition, "cerrada", "TRANSACTION_NOT_ACTIVE")
	})
	te.OnAcquire(engine, queuedForever)
	started := time.Now()
	err := connect(t, engine).WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		_, err := tx.AcquireLock(ctx, "ns", "k", Exclusive, &AcquireLockOptions{Timeout: 8 * time.Second})
		return err
	}, life(2*time.Second))
	te.As[*Error](t, err)
	te.As[*TransactionNotActiveError](t, err)
	te.True(t, time.Since(started) < 4*time.Second, "took %v", time.Since(started))
}

func TestLosingTheTransactionAlsoCutsAWaitThatBringsItsOwnContext(t *testing.T) {
	engine, _ := scripted(t)
	te.OnDLS(engine, "renewTransaction", func(context.Context, *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
		return nil, te.WithReason(codes.FailedPrecondition, "cerrada", "TRANSACTION_NOT_ACTIVE")
	})
	te.OnAcquire(engine, queuedForever)
	own, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	err := connect(t, engine).WithTransaction(ctx, func(_ context.Context, tx Tx) error {
		_, err := tx.AcquireLock(own, "ns", "k", Exclusive, &AcquireLockOptions{Timeout: 8 * time.Second})
		return err
	}, life(2*time.Second))
	te.As[*Error](t, err)
	te.True(t, time.Since(started) < 4*time.Second, "took %v", time.Since(started))
	te.True(t, own.Err() == nil, "the caller context must stay untouched")
}

func TestItsOwnContextStillCutsTheWaitInsideATransaction(t *testing.T) {
	engine, _ := scripted(t)
	te.OnAcquire(engine, queuedForever)
	own, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	err := connect(t, engine).WithTransaction(ctx, func(_ context.Context, tx Tx) error {
		_, err := tx.AcquireLock(own, "ns", "k", Exclusive, &AcquireLockOptions{Timeout: 8 * time.Second})
		return err
	}, noRenew)
	te.Contains(t, te.As[*Error](t, err).Error(), "AcquireLock aborted by user")
	te.True(t, errors.Is(err, context.Canceled), "the cancellation should stay reachable")
}

func TestASlowRenewalIsNeverOverlappedByTheNextOne(t *testing.T) {
	engine, _ := scripted(t)
	var inFlight, maxInFlight, started atomic.Int32
	te.OnDLS(engine, "renewTransaction", func(context.Context, *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
		started.Add(1)
		now := inFlight.Add(1)
		for {
			seen := maxInFlight.Load()
			if now <= seen || maxInFlight.CompareAndSwap(seen, now) {
				break
			}
		}
		time.Sleep(2500 * time.Millisecond)
		inFlight.Add(-1)
		return &dlspb.RenewTransactionResponse{TransactionId: "tx-1"}, nil
	})
	te.NoError(t, connect(t, engine, func(o *ClientOptions) { o.Timeout = 5 * time.Second }).WithTransaction(ctx, func(context.Context, Tx) error {
		time.Sleep(3800 * time.Millisecond)
		return nil
	}, life(2*time.Second)))
	te.Equal(t, maxInFlight.Load(), int32(1))
	te.True(t, started.Load() >= 1 && started.Load() <= 2, "started %d renewals", started.Load())
}

func TestOnceClosedTheContextCannotBeUsed(t *testing.T) {
	engine, _ := scripted(t)
	var leaked Tx
	te.NoError(t, connect(t, engine).WithTransaction(ctx, func(_ context.Context, tx Tx) error {
		leaked = tx
		return nil
	}, noRenew))
	_, err := leaked.AcquireLock(ctx, "ns", "k", Exclusive, nil)
	te.True(t, errors.Is(err, ErrTransactionClosed), "got %v", err)
	te.Equal(t, err.Error(), "transaction context already closed")
	_, err = leaked.RenewTransaction(ctx, time.Second)
	te.True(t, errors.Is(err, ErrTransactionClosed), "got %v", err)
}

func TestTheContextRenewsOnRequest(t *testing.T) {
	engine, calls := scripted(t)
	te.NoError(t, connect(t, engine).WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		_, err := tx.RenewTransaction(ctx, 5*time.Second)
		return err
	}, noRenew))
	te.Equal(t, calls.list(), []string{"begin", "renew", "releaseAll"})
}

func TestWaitsInTheQueueAndResolvesWhenTheLockIsGranted(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		_ = stream.Send(te.WithStatus(dlspb.LockStatus_QUEUED))
		time.Sleep(60 * time.Millisecond)
		_ = stream.Send(te.WithStatus(dlspb.LockStatus_QUEUED))
		time.Sleep(60 * time.Millisecond)
		return stream.Send(te.Acquired("lock-9", 42))
	})
	lock := te.Must(connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, &AcquireLockOptions{Timeout: 5 * time.Second}))
	te.Equal(t, *lock, LockHolder{LockID: "lock-9", FencingToken: 42, Status: Acquired})
}

func TestAnErrorHalfwayThroughTheStreamArrivesTypedAndWithItsCode(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		_ = stream.Send(te.WithStatus(dlspb.LockStatus_QUEUED))
		time.Sleep(50 * time.Millisecond)
		return te.Status(codes.Aborted, "la transaccion se aborto")
	})
	started := time.Now()
	_, err := connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, &AcquireLockOptions{Timeout: 5 * time.Second})
	conflict := te.As[*ConflictError](t, err)
	te.Equal(t, conflict.Code(), caerus.CodeConflict)
	te.True(t, conflict.RequestID() != "", "missing request id")
	te.True(t, time.Since(started) < 2*time.Second, "took %v", time.Since(started))
}

func TestAStreamThatEndsWithoutATerminalStatusIsNotTakenAsGranted(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		_ = stream.Send(te.WithStatus(dlspb.LockStatus_QUEUED))
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	_, err := connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, &AcquireLockOptions{Timeout: 5 * time.Second})
	dlsErr := te.As[*Error](t, err)
	te.Contains(t, dlsErr.Error(), "Stream ended without terminal status")
	te.Equal(t, dlsErr.Code(), caerus.CodeUnknown)
	te.True(t, dlsErr.RequestID() != "", "missing request id")
}

func TestCancellingTheContextCutsAWaitInProgress(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, queuedForever)
	cancellable, cancel := context.WithCancel(ctx)
	time.AfterFunc(80*time.Millisecond, cancel)
	started := time.Now()
	_, err := connect(t, engine).AcquireLock(cancellable, "ns", "k", "tx-1", Exclusive, &AcquireLockOptions{Timeout: 5 * time.Second})
	te.Contains(t, te.As[*Error](t, err).Error(), "AcquireLock aborted by user")
	te.True(t, errors.Is(err, context.Canceled), "the cancellation should stay reachable")
	te.True(t, time.Since(started) < 2*time.Second, "took %v", time.Since(started))
}

func TestAnAlreadyCancelledContextFailsWithoutCalling(t *testing.T) {
	engine := te.StartDLS(t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := connect(t, engine).AcquireLock(cancelled, "ns", "k", "tx-1", Exclusive, nil)
	te.Contains(t, te.As[*Error](t, err).Error(), "AcquireLock aborted by user")
	te.Equal(t, engine.LastMethod(), "")
}

func TestACallerDeadlineOnTheContextIsATimeout(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, queuedForever)
	bounded, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	_, err := connect(t, engine).AcquireLock(bounded, "ns", "k", "tx-1", Exclusive, nil)
	te.As[*TimeoutError](t, err)
}

func TestWithoutItsOwnTimeoutAQueuedLockUsesTheClientDeadline(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, queuedForever)
	impatient := connect(t, engine, func(o *ClientOptions) { o.Timeout = 300 * time.Millisecond })
	_, err := impatient.AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil)
	te.As[*TimeoutError](t, err)
}

func TestItsOwnTimeoutBeatsTheClientDeadline(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		_ = stream.Send(te.WithStatus(dlspb.LockStatus_QUEUED))
		time.Sleep(600 * time.Millisecond)
		return stream.Send(te.Acquired("lock-1", 1))
	})
	impatient := connect(t, engine, func(o *ClientOptions) { o.Timeout = 300 * time.Millisecond })
	lock := te.Must(impatient.AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, &AcquireLockOptions{Timeout: 3 * time.Second}))
	te.Equal(t, lock.LockID, "lock-1")
}

func TestAClosedClientRejectsInsteadOfHanging(t *testing.T) {
	engine := te.StartDLS(t)
	client := connect(t, engine)
	te.NoError(t, client.Close())
	_, err := client.BeginTransaction(ctx, nil)
	te.Contains(t, te.As[*Error](t, err).Error(), "closed")
	_, err = client.AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil)
	te.Contains(t, te.As[*Error](t, err).Error(), "closed")
	te.NoError(t, client.Close())
}

func TestTheRenewalGoroutineEndsWithTheTransaction(t *testing.T) {
	engine, _ := scripted(t)
	client := connect(t, engine)
	var held *scope
	te.NoError(t, client.WithTransaction(ctx, func(_ context.Context, tx Tx) error {
		held = tx.(*scope)
		time.Sleep(50 * time.Millisecond)
		return nil
	}, life(2*time.Second)))
	select {
	case <-held.done:
	default:
		t.Fatal("the renewal goroutine is still running")
	}
}

func TestSeveralGoroutinesCanHoldTransactionsAtOnce(t *testing.T) {
	engine, calls := scripted(t)
	client := connect(t, engine)
	var wg sync.WaitGroup
	results := make(chan string, 8)
	for index := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = client.WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
				lock, err := tx.AcquireLock(ctx, "ns", fmt.Sprintf("k%d", index), Exclusive, nil)
				if err == nil {
					results <- lock.LockID
				}
				return err
			}, noRenew)
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for lockID := range results {
		te.Equal(t, lockID, "lock-1")
		count++
	}
	te.Equal(t, count, 8)
	te.Equal(t, calls.count("releaseAll"), 8)
}
