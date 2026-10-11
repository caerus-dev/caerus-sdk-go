package dls

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultLifetime    = 10 * time.Second
	minRenewalInterval = time.Second
)

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
	return nil
}

func lifetime(options TransactionOptions) time.Duration {
	if options.Timeout > 0 {
		return options.Timeout
	}
	return defaultLifetime
}

func renewalInterval(lifetime time.Duration) time.Duration {
	return max(minRenewalInterval, lifetime/2)
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
	return s.ops.RenewTransaction(ctx, s.transactionID, extra)
}

func (s *scope) heartbeat(ctx context.Context) {
	defer close(s.done)
	life := lifetime(s.options)
	ticker := time.NewTicker(renewalInterval(life))
	defer ticker.Stop()
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
		case <-ticker.C:
		}
		_, err := s.ops.RenewTransaction(renewCtx, s.transactionID, life)
		if s.isClosed() {
			return
		}
		if err == nil {
			failures = 0
			continue
		}
		failures++
		if isGone(err) || failures >= 2 {
			s.giveUp(err)
			return
		}
	}
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
