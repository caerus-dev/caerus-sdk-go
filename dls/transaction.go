package dls

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
)

const defaultLifetime = 10 * time.Second

type operations interface {
	BeginTransaction(ctx context.Context, options *BeginTransactionOptions) (*Transaction, error)
	AcquireLock(ctx context.Context, namespace, lockKey, transactionID string, mode LockMode, options *AcquireLockOptions) (*LockHolder, error)
	RenewTransaction(ctx context.Context, transactionID string, extra time.Duration) (*Transaction, error)
	ReleaseTransactionLocks(ctx context.Context, transactionID string) error
	logger() *slog.Logger
}

type scope struct {
	ops           operations
	transactionID string
	options       TransactionOptions
	ctx           context.Context
	cancel        context.CancelCauseFunc
	mu            sync.Mutex
	closed        bool
	lost          error
	expiresAt     time.Time
	stop          chan struct{}
	done          chan struct{}
	stopOnce      sync.Once
}

func runTransaction(ctx context.Context, ops operations, fn TransactionFunc, options *TransactionOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var resolved TransactionOptions
	if options != nil {
		resolved = *options
	}
	started := time.Now()
	transaction, err := ops.BeginTransaction(ctx, &BeginTransactionOptions{Timeout: resolved.Timeout})
	if err != nil {
		return err
	}
	scopeCtx, cancel := context.WithCancelCause(ctx)
	s := &scope{
		ops:           ops,
		transactionID: transaction.TransactionID,
		options:       resolved,
		ctx:           scopeCtx,
		expiresAt:     started.Add(lifetime(resolved)),
		cancel:        cancel,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	if resolved.DisableAutoRenew {
		close(s.done)
	} else {
		go s.heartbeat(context.WithoutCancel(ctx))
	}
	defer func() {
		s.close()
		_ = ops.ReleaseTransactionLocks(context.WithoutCancel(ctx), s.transactionID)
		cancel(context.Canceled)
	}()
	if err := fn(scopeCtx, s); err != nil {
		return err
	}
	if lost := s.lostError(); lost != nil {
		return lost
	}
	if s.expiredWhileRunning() {
		return s.expiredError(nil)
	}
	return nil
}

func lifetime(options TransactionOptions) time.Duration {
	if options.Timeout > 0 {
		return options.Timeout
	}
	return defaultLifetime
}

func renewalInterval(lifetime time.Duration) time.Duration {
	return lifetime / 2
}

func (s *scope) TransactionID() string {
	return s.transactionID
}

func (s *scope) String() string {
	return "dls.Tx[" + s.transactionID + "]"
}

func (s *scope) AcquireLock(ctx context.Context, namespace, lockKey string, mode LockMode, options *AcquireLockOptions) (*LockHolder, error) {
	if ctx == nil {
		ctx = s.ctx
	}
	s.mu.Lock()
	closed, lost := s.closed, s.lost
	s.mu.Unlock()
	if closed {
		return nil, ErrTransactionClosed
	}
	if lost != nil {
		return nil, lost
	}
	merged, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := context.AfterFunc(s.ctx, func() {
		cancel(context.Cause(s.ctx))
	})
	defer stop()
	return s.ops.AcquireLock(merged, namespace, lockKey, s.transactionID, mode, options)
}

func (s *scope) RenewTransaction(ctx context.Context, extra time.Duration) (*Transaction, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrTransactionClosed
	}
	if ctx == nil {
		ctx = s.ctx
	}
	transaction, err := s.ops.RenewTransaction(ctx, s.transactionID, extra)
	if err == nil {
		s.extend(extra)
	}
	return transaction, err
}

func (s *scope) heartbeat(ctx context.Context) {
	defer close(s.done)
	life := lifetime(s.options)
	interval := renewalInterval(life)
	s.mu.Lock()
	first := max(0, time.Until(s.expiresAt.Add(interval-life)))
	s.mu.Unlock()
	timer := time.NewTimer(first)
	defer timer.Stop()
	renewCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.stop:
			cancel()
		case <-renewCtx.Done():
		}
	}()
	failures := 0
	for {
		select {
		case <-s.stop:
			return
		case <-timer.C:
		}
		err := s.renewOnce(renewCtx, life)
		if s.isClosed() {
			return
		}
		if err == nil {
			failures = 0
			timer.Reset(interval)
			continue
		}
		failures++
		if isGone(err) || failures >= 2 || !s.alive() {
			s.giveUp(s.lossOf(err))
			return
		}
		timer.Reset(interval / 4)
	}
}

func (s *scope) renewOnce(ctx context.Context, life time.Duration) error {
	s.mu.Lock()
	remaining := time.Until(s.expiresAt)
	s.mu.Unlock()
	if remaining <= 0 {
		return s.expiredError(nil)
	}
	extra := life - remaining
	if extra < time.Millisecond {
		return nil
	}
	attempt, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	if _, err := s.ops.RenewTransaction(attempt, s.transactionID, extra); err != nil {
		return err
	}
	s.extend(extra)
	return nil
}

func (s *scope) extend(extra time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expiresAt = s.expiresAt.Add(time.Duration(millis(extra)) * time.Millisecond)
}

func (s *scope) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.expiresAt)
}

func (s *scope) expiredWhileRunning() bool {
	if s.options.DisableAutoRenew && s.options.Timeout <= 0 {
		return false
	}
	return !s.alive()
}

func (s *scope) lossOf(err error) error {
	if isGone(err) || s.alive() {
		return err
	}
	return s.expiredError(err)
}

func (s *scope) expiredError(cause error) error {
	return NewTransactionNotActiveError(
		"Transaction "+s.transactionID+" expired before it could be renewed",
		&caerus.ErrorOptions{Cause: cause})
}

func (s *scope) giveUp(err error) {
	s.mu.Lock()
	if s.closed || s.lost != nil {
		s.mu.Unlock()
		return
	}
	s.lost = err
	s.mu.Unlock()
	s.cancel(err)
	if s.options.OnTransactionLost == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			s.ops.logger().Error("OnTransactionLost callback threw", "panic", recovered)
		}
	}()
	s.options.OnTransactionLost(err)
}

func (s *scope) lostError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lost
}

func (s *scope) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *scope) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
}
