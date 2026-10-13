package dls

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/config"
	"github.com/caerus-dev/caerus-sdk-go/internal/logging"
)

const (
	defaultMockLifetime = 10 * time.Second
	expiredStatus       = "MAX_LIFETIME_EXCEEDED"
)

type mockLock struct {
	lockID         string
	transactionID  string
	namespace      string
	lockKey        string
	mode           LockMode
	fencingToken   int64
	expiresAt      int64
	idempotencyKey string
}

type InMemoryClient struct {
	mu                 sync.Mutex
	clock              func() time.Time
	log                *slog.Logger
	activeTransactions map[string]bool
	locks              map[string][]*mockLock
	lockOrder          []string
	txExpiration       map[string]int64
	nextLockID         int64
	nextFencingToken   int64
	closed             bool
}

func NewInMemoryClient() *InMemoryClient {
	return newInMemoryClient(time.Now)
}

func newInMemoryClient(clock func() time.Time) *InMemoryClient {
	return &InMemoryClient{
		clock:              clock,
		log:                logging.New(config.DLS.LoggerPrefix),
		activeTransactions: map[string]bool{},
		locks:              map[string][]*mockLock{},
		txExpiration:       map[string]int64{},
		nextLockID:         1,
		nextFencingToken:   1000,
	}
}

func (m *InMemoryClient) WithTransaction(ctx context.Context, fn TransactionFunc, options *TransactionOptions) error {
	return runTransaction(ctx, m, fn, options)
}

func (m *InMemoryClient) BeginTransaction(_ context.Context, options *BeginTransactionOptions) (*Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return nil, err
	}
	now := m.nowMs()
	transactionID := fmt.Sprintf("tx-mock-%d-%s", now, randomSuffix())
	m.activeTransactions[transactionID] = true
	life := defaultMockLifetime
	if options != nil && options.Timeout > 0 {
		life = options.Timeout
	}
	m.txExpiration[transactionID] = now + millis(life)
	return &Transaction{TransactionID: transactionID}, nil
}

func (m *InMemoryClient) AcquireLock(ctx context.Context, namespace, lockKey, transactionID string, mode LockMode, options *AcquireLockOptions) (*LockHolder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return nil, err
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, abortedError("", context.Cause(ctx))
	}
	if options == nil {
		options = &AcquireLockOptions{}
	}
	if !m.activeTransactions[transactionID] {
		return nil, NewNotFoundError("Transaction not found or expired", nil)
	}
	if err := m.requireAlive(transactionID); err != nil {
		return nil, err
	}
	if mode != SharedRead {
		mode = Exclusive
	}
	key := namespace + ":" + lockKey
	now := m.nowMs()
	active := m.activeLocks(key, now)
	for _, held := range active {
		if held.transactionID != transactionID {
			continue
		}
		if options.IdempotencyKey != "" && options.IdempotencyKey == held.idempotencyKey {
			return &LockHolder{LockID: held.lockID, FencingToken: held.fencingToken, Status: Acquired}, nil
		}
		return nil, NewLockAlreadyHeldError(
			"Transaction "+transactionID+" already holds or requested lock on "+lockKey,
			&caerus.ErrorOptions{Reason: caerus.ReasonLockAlreadyHeldExclusively})
	}
	if len(active) > 0 && (mode == Exclusive || slices.ContainsFunc(active, func(held *mockLock) bool { return held.mode == Exclusive })) {
		return nil, NewLockDeniedError(
			"Caerus denied the lock on "+namespace+"/"+lockKey+": it is already held by another transaction.",
			&caerus.ErrorOptions{Reason: caerus.ReasonLockDenied})
	}
	expiresAt, ok := m.txExpiration[transactionID]
	if !ok {
		expiresAt = now + millis(defaultMockLifetime)
	}
	created := &mockLock{
		lockID:         "lock-mock-" + strconv.FormatInt(m.nextLockID, 10),
		transactionID:  transactionID,
		namespace:      namespace,
		lockKey:        lockKey,
		mode:           mode,
		fencingToken:   m.nextFencingToken,
		expiresAt:      expiresAt,
		idempotencyKey: options.IdempotencyKey,
	}
	m.nextLockID++
	m.nextFencingToken++
	if _, exists := m.locks[key]; !exists {
		m.lockOrder = append(m.lockOrder, key)
	}
	m.locks[key] = append(active, created)
	return &LockHolder{LockID: created.lockID, FencingToken: created.fencingToken, Status: Acquired}, nil
}

func (m *InMemoryClient) RenewTransaction(_ context.Context, transactionID string, extra time.Duration) (*Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return nil, err
	}
	if !m.activeTransactions[transactionID] {
		return nil, NewNotFoundError("Transaction not found", nil)
	}
	if err := m.requireAlive(transactionID); err != nil {
		return nil, err
	}
	extraMs := millis(extra)
	current, ok := m.txExpiration[transactionID]
	if !ok {
		current = m.nowMs()
	}
	m.txExpiration[transactionID] = current + extraMs
	for _, key := range m.lockOrder {
		for _, held := range m.locks[key] {
			if held.transactionID == transactionID {
				held.expiresAt += extraMs
			}
		}
	}
	return &Transaction{TransactionID: transactionID}, nil
}

func (m *InMemoryClient) ReleaseLock(_ context.Context, lockID, transactionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	for _, key := range m.lockOrder {
		held := m.locks[key]
		index := slices.IndexFunc(held, func(lock *mockLock) bool {
			return lock.lockID == lockID && lock.transactionID == transactionID
		})
		if index >= 0 {
			m.locks[key] = slices.Delete(held, index, index+1)
			return nil
		}
	}
	return nil
}

func (m *InMemoryClient) ReleaseTransactionLocks(_ context.Context, transactionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return err
	}
	for _, key := range m.lockOrder {
		m.locks[key] = slices.DeleteFunc(m.locks[key], func(lock *mockLock) bool { return lock.transactionID == transactionID })
	}
	delete(m.activeTransactions, transactionID)
	delete(m.txExpiration, transactionID)
	return nil
}

func (m *InMemoryClient) GetLockStatus(_ context.Context, namespace, lockKey string) (*LockStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return nil, err
	}
	active := m.activeLocks(namespace+":"+lockKey, m.nowMs())
	response := &LockStatusResponse{IsHeld: len(active) > 0, ActiveHolders: []ActiveLockHolder{}}
	if len(active) > 0 {
		response.CurrentMode = active[0].mode
	}
	for _, held := range active {
		response.ActiveHolders = append(response.ActiveHolders, ActiveLockHolder{
			LockID:       held.lockID,
			ExpiresAt:    held.expiresAt,
			FencingToken: held.fencingToken,
		})
	}
	return response, nil
}

func (m *InMemoryClient) GetTransactionStatus(_ context.Context, transactionID string) (*TransactionStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(); err != nil {
		return nil, err
	}
	if !m.activeTransactions[transactionID] {
		return nil, NewNotFoundError("Transaction not found", nil)
	}
	response := &TransactionStatusResponse{Status: "ACTIVE", Locks: []TransactionLockInfo{}, ExpiresAt: m.txExpiration[transactionID]}
	if m.expired(transactionID) {
		response.Status = expiredStatus
		return response, nil
	}
	for _, key := range m.lockOrder {
		for _, held := range m.locks[key] {
			if held.transactionID == transactionID {
				response.Locks = append(response.Locks, TransactionLockInfo{
					Namespace:     held.namespace,
					LockKey:       held.lockKey,
					RequestedMode: held.mode,
					Status:        Acquired,
				})
			}
		}
	}
	return response, nil
}

func (m *InMemoryClient) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *InMemoryClient) logger() *slog.Logger {
	return m.log
}

func (m *InMemoryClient) activeLocks(key string, now int64) []*mockLock {
	var active []*mockLock
	for _, held := range m.locks[key] {
		if held.expiresAt > now {
			active = append(active, held)
		}
	}
	return active
}

func (m *InMemoryClient) expired(transactionID string) bool {
	expiresAt, ok := m.txExpiration[transactionID]
	return ok && expiresAt <= m.nowMs()
}

func (m *InMemoryClient) requireAlive(transactionID string) error {
	if !m.expired(transactionID) {
		return nil
	}
	return NewTransactionNotActiveError(
		"Transaction "+transactionID+" is already "+expiredStatus+".",
		&caerus.ErrorOptions{Reason: caerus.ReasonTransactionNotActive})
}

func (m *InMemoryClient) guard() error {
	if m.closed {
		return closedError("dls.InMemoryClient")
	}
	return nil
}

func (m *InMemoryClient) nowMs() int64 {
	return m.clock().UnixMilli()
}

func randomSuffix() string {
	limit := big.NewInt(78364164096)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "0"
	}
	return strconv.FormatInt(value.Int64(), 36)
}
