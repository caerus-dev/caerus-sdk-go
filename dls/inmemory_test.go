package dls

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

func begin(t *testing.T, mock *InMemoryClient) string {
	t.Helper()
	return te.Must(mock.BeginTransaction(ctx, nil)).TransactionID
}

func TestTheMockIsUsableWhereverTheRealClientIs(t *testing.T) {
	var api API = NewInMemoryClient()
	te.NoError(t, api.WithTransaction(ctx, func(context.Context, Tx) error { return nil }, noRenew))
	te.NoError(t, api.Close())
}

func TestCanBeginATransaction(t *testing.T) {
	mock := NewInMemoryClient()
	transactionID := begin(t, mock)
	te.True(t, strings.HasPrefix(transactionID, "tx-mock-"), "id %q", transactionID)
	te.Equal(t, te.Must(mock.GetTransactionStatus(ctx, transactionID)).Status, "ACTIVE")
}

func TestReadingATransactionThatDoesNotExistIsNotFound(t *testing.T) {
	_, err := NewInMemoryClient().GetTransactionStatus(ctx, "tx-nope")
	te.As[*NotFoundError](t, err)
}

func TestCanAcquireAndReleaseAnExclusiveLockWithIncreasingFencingTokens(t *testing.T) {
	mock := NewInMemoryClient()
	tx := begin(t, mock)
	first := te.Must(mock.AcquireLock(ctx, "ns", "a", tx, Exclusive, nil))
	second := te.Must(mock.AcquireLock(ctx, "ns", "b", tx, Exclusive, nil))
	te.Equal(t, first.Status, Acquired)
	te.True(t, second.FencingToken > first.FencingToken, "tokens %d then %d", first.FencingToken, second.FencingToken)
	te.Equal(t, te.Must(mock.GetLockStatus(ctx, "ns", "a")).IsHeld, true)
	te.NoError(t, mock.ReleaseLock(ctx, first.LockID, tx))
	te.Equal(t, te.Must(mock.GetLockStatus(ctx, "ns", "a")).IsHeld, false)
}

func TestDeniesAnExclusiveLockAlreadyHeldByAnotherTransaction(t *testing.T) {
	mock := NewInMemoryClient()
	te.Must(mock.AcquireLock(ctx, "ns", "k", begin(t, mock), Exclusive, nil))
	_, err := mock.AcquireLock(ctx, "ns", "k", begin(t, mock), Exclusive, nil)
	denied := te.As[*LockDeniedError](t, err)
	te.Equal(t, denied.Reason(), caerus.ReasonLockDenied)
}

func TestSharedReadLivesWithSharedReadButNotWithExclusive(t *testing.T) {
	mock := NewInMemoryClient()
	te.Must(mock.AcquireLock(ctx, "ns", "file", begin(t, mock), SharedRead, nil))
	te.Must(mock.AcquireLock(ctx, "ns", "file", begin(t, mock), SharedRead, nil))
	status := te.Must(mock.GetLockStatus(ctx, "ns", "file"))
	te.Equal(t, len(status.ActiveHolders), 2)
	te.Equal(t, status.CurrentMode, SharedRead)
	_, err := mock.AcquireLock(ctx, "ns", "file", begin(t, mock), Exclusive, nil)
	te.As[*LockDeniedError](t, err)
	te.Must(mock.AcquireLock(ctx, "ns", "other", begin(t, mock), Exclusive, nil))
	_, err = mock.AcquireLock(ctx, "ns", "other", begin(t, mock), SharedRead, nil)
	te.As[*LockDeniedError](t, err)
}

func TestAnUnspecifiedModeIsTreatedAsExclusiveLikeTheEngine(t *testing.T) {
	mock := NewInMemoryClient()
	te.Must(mock.AcquireLock(ctx, "ns", "k", begin(t, mock), SharedRead, nil))
	_, err := mock.AcquireLock(ctx, "ns", "k", begin(t, mock), "", nil)
	te.As[*LockDeniedError](t, err)
}

func TestCanReleaseAllTheLocksOfATransactionAtOnceAndClosesIt(t *testing.T) {
	mock := NewInMemoryClient()
	tx := begin(t, mock)
	te.Must(mock.AcquireLock(ctx, "ns", "a", tx, Exclusive, nil))
	te.Must(mock.AcquireLock(ctx, "ns", "b", tx, Exclusive, nil))
	te.NoError(t, mock.ReleaseTransactionLocks(ctx, tx))
	te.Equal(t, te.Must(mock.GetLockStatus(ctx, "ns", "a")).IsHeld, false)
	te.Equal(t, te.Must(mock.GetLockStatus(ctx, "ns", "b")).IsHeld, false)
	_, err := mock.GetTransactionStatus(ctx, tx)
	te.As[*NotFoundError](t, err)
}

func TestCanRenewATransactionAndItsLocks(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	mock := newInMemoryClient(func() time.Time { return now })
	tx := begin(t, mock)
	te.Must(mock.AcquireLock(ctx, "ns", "k", tx, Exclusive, nil))
	te.Must(mock.RenewTransaction(ctx, tx, 5*time.Second))
	te.Equal(t, te.Must(mock.GetTransactionStatus(ctx, tx)).ExpiresAt, int64(1_000_000+10_000+5_000))
	te.Equal(t, te.Must(mock.GetLockStatus(ctx, "ns", "k")).ActiveHolders[0].ExpiresAt, int64(1_000_000+10_000+5_000))
	_, err := mock.RenewTransaction(ctx, "tx-nope", time.Second)
	te.As[*NotFoundError](t, err)
}

func TestALockOfAnExpiredTransactionNoLongerCounts(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	mock := newInMemoryClient(func() time.Time { return now })
	te.Must(mock.AcquireLock(ctx, "ns", "k", begin(t, mock), Exclusive, nil))
	now = now.Add(11 * time.Second)
	te.Equal(t, te.Must(mock.GetLockStatus(ctx, "ns", "k")).IsHeld, false)
	te.Must(mock.AcquireLock(ctx, "ns", "k", begin(t, mock), Exclusive, nil))
}

func TestReleasingWhatDoesNotExistOrIsSomeoneElsesDoesNothingLikeTheEngine(t *testing.T) {
	mock := NewInMemoryClient()
	owner := begin(t, mock)
	lock := te.Must(mock.AcquireLock(ctx, "ns", "k", owner, Exclusive, nil))
	te.NoError(t, mock.ReleaseLock(ctx, "lock-nope", owner))
	te.NoError(t, mock.ReleaseLock(ctx, lock.LockID, "tx-nope"))
	te.NoError(t, mock.ReleaseLock(ctx, lock.LockID, begin(t, mock)))
	te.Equal(t, te.Must(mock.GetLockStatus(ctx, "ns", "k")).IsHeld, true)
}

func TestGetTransactionStatusReturnsEveryLockOfTheTransaction(t *testing.T) {
	mock := NewInMemoryClient()
	tx := begin(t, mock)
	te.Must(mock.AcquireLock(ctx, "ns", "a", tx, Exclusive, nil))
	te.Must(mock.AcquireLock(ctx, "ns", "b", tx, SharedRead, nil))
	te.Must(mock.AcquireLock(ctx, "ns", "c", begin(t, mock), Exclusive, nil))
	te.Equal(t, te.Must(mock.GetTransactionStatus(ctx, tx)).Locks, []TransactionLockInfo{
		{Namespace: "ns", LockKey: "a", RequestedMode: Exclusive, Status: Acquired},
		{Namespace: "ns", LockKey: "b", RequestedMode: SharedRead, Status: Acquired},
	})
}

func TestTheSameIdempotencyKeyReplaysTheSameLockLikeTheEngine(t *testing.T) {
	mock := NewInMemoryClient()
	tx := begin(t, mock)
	first := te.Must(mock.AcquireLock(ctx, "ns", "k", tx, Exclusive, &AcquireLockOptions{IdempotencyKey: "idem"}))
	again := te.Must(mock.AcquireLock(ctx, "ns", "k", tx, Exclusive, &AcquireLockOptions{IdempotencyKey: "idem"}))
	te.Equal(t, *again, *first)
	_, err := mock.AcquireLock(ctx, "ns", "k", tx, Exclusive, &AcquireLockOptions{IdempotencyKey: "other"})
	held := te.As[*LockAlreadyHeldError](t, err)
	te.Equal(t, held.Reason(), caerus.ReasonLockAlreadyHeldExclusively)
	_, err = mock.AcquireLock(ctx, "ns", "k", tx, Exclusive, nil)
	te.As[*LockAlreadyHeldError](t, err)
}

func TestTheIdempotencyKeyOfAnotherTransactionDoesNotReplay(t *testing.T) {
	mock := NewInMemoryClient()
	te.Must(mock.AcquireLock(ctx, "ns", "k", begin(t, mock), Exclusive, &AcquireLockOptions{IdempotencyKey: "idem"}))
	_, err := mock.AcquireLock(ctx, "ns", "k", begin(t, mock), Exclusive, &AcquireLockOptions{IdempotencyKey: "idem"})
	te.As[*LockDeniedError](t, err)
}

func TestRefusesALockForATransactionThatDoesNotExist(t *testing.T) {
	_, err := NewInMemoryClient().AcquireLock(ctx, "ns", "k", "tx-nope", Exclusive, nil)
	te.As[*NotFoundError](t, err)
}

func TestWithTransactionReleasesAtTheEndAndOnError(t *testing.T) {
	mock := NewInMemoryClient()
	te.NoError(t, mock.WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		_, err := tx.AcquireLock(ctx, "ns", "k", Exclusive, nil)
		return err
	}, nil))
	te.Equal(t, te.Must(mock.GetLockStatus(ctx, "ns", "k")).IsHeld, false)
	broken := errors.New("roto")
	err := mock.WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		if _, err := tx.AcquireLock(ctx, "ns", "k", Exclusive, nil); err != nil {
			return err
		}
		return broken
	}, nil)
	te.True(t, err == broken, "got %v", err)
	te.Equal(t, te.Must(mock.GetLockStatus(ctx, "ns", "k")).IsHeld, false)
}

func TestWithTransactionRenewsOnItsOwn(t *testing.T) {
	mock := NewInMemoryClient()
	var before, after int64
	te.NoError(t, mock.WithTransaction(ctx, func(_ context.Context, tx Tx) error {
		before = te.Must(mock.GetTransactionStatus(ctx, tx.TransactionID())).ExpiresAt
		time.Sleep(1300 * time.Millisecond)
		after = te.Must(mock.GetTransactionStatus(ctx, tx.TransactionID())).ExpiresAt
		return nil
	}, life(2*time.Second)))
	te.Equal(t, after-before, int64(2000))
}

func TestPreventsAcquireLockOnceTheContextIsClosed(t *testing.T) {
	mock := NewInMemoryClient()
	var leaked Tx
	te.NoError(t, mock.WithTransaction(ctx, func(_ context.Context, tx Tx) error {
		leaked = tx
		return nil
	}, nil))
	_, err := leaked.AcquireLock(ctx, "ns", "k", Exclusive, nil)
	te.True(t, errors.Is(err, ErrTransactionClosed), "got %v", err)
}

func TestAbortsAcquireLockIfTheContextIsCancelled(t *testing.T) {
	mock := NewInMemoryClient()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := mock.AcquireLock(cancelled, "ns", "k", begin(t, mock), Exclusive, nil)
	te.Contains(t, te.As[*Error](t, err).Error(), "AcquireLock aborted by user")
}

func TestIfRenewingFailsBeyondRepairTheTransactionIsLost(t *testing.T) {
	mock := NewInMemoryClient()
	var lost atomic.Value
	var onTake error
	err := mock.WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		te.NoError(t, mock.ReleaseTransactionLocks(ctx, tx.TransactionID()))
		time.Sleep(1300 * time.Millisecond)
		_, onTake = tx.AcquireLock(ctx, "ns", "k", Exclusive, nil)
		return nil
	}, &TransactionOptions{Timeout: 2 * time.Second, OnTransactionLost: func(err error) { lost.Store(err) }})
	te.As[*NotFoundError](t, err)
	te.As[*NotFoundError](t, onTake)
	te.True(t, lost.Load() == err, "OnTransactionLost got a different error")
}

func TestRejectsCallsAfterBeingClosed(t *testing.T) {
	mock := NewInMemoryClient()
	te.NoError(t, mock.Close())
	_, err := mock.BeginTransaction(ctx, nil)
	te.Contains(t, te.As[*Error](t, err).Error(), "has been closed")
	te.Contains(t, te.As[*Error](t, mock.ReleaseLock(ctx, "l", "t")).Error(), "has been closed")
}
