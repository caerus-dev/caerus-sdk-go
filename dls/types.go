package dls

import (
	"context"
	"log/slog"
	"time"
)

type LockMode string

const (
	Exclusive  LockMode = "EXCLUSIVE"
	SharedRead LockMode = "SHARED_READ"
)

type LockStatus string

const (
	Acquired      LockStatus = "ACQUIRED"
	Denied        LockStatus = "DENIED"
	Queued        LockStatus = "QUEUED"
	UnknownStatus LockStatus = "UNKNOWN"
)

type Transaction struct {
	TransactionID string
}

type LockHolder struct {
	LockID       string
	FencingToken int64
	Status       LockStatus
}

type ActiveLockHolder struct {
	LockID       string
	ExpiresAt    int64
	FencingToken int64
}

type LockStatusResponse struct {
	IsHeld           bool
	CurrentMode      LockMode
	ActiveHolders    []ActiveLockHolder
	PendingQueueSize int
}

type TransactionLockInfo struct {
	Namespace     string
	LockKey       string
	RequestedMode LockMode
	Status        LockStatus
}

type TransactionStatusResponse struct {
	Status      string
	AbortReason string
	Locks       []TransactionLockInfo
	ExpiresAt   int64
}

type ClientOptions struct {
	APIKey   string
	Endpoint string
	TLS      *bool
	Timeout  time.Duration
	Logger   *slog.Logger
}

func (o ClientOptions) String() string {
	return "dls.ClientOptions{Endpoint: " + o.Endpoint + ", Timeout: " + o.Timeout.String() + "}"
}

func (o ClientOptions) GoString() string {
	return o.String()
}

type BeginTransactionOptions struct {
	Timeout time.Duration
}

type AcquireLockOptions struct {
	IdempotencyKey string
	Timeout        time.Duration
	OnQueued       func()
}

type TransactionOptions struct {
	Timeout           time.Duration
	DisableAutoRenew  bool
	OnTransactionLost func(err error)
}

type Tx interface {
	TransactionID() string
	AcquireLock(ctx context.Context, namespace, lockKey string, mode LockMode, options *AcquireLockOptions) (*LockHolder, error)
	RenewTransaction(ctx context.Context, extra time.Duration) (*Transaction, error)
}

type TransactionFunc func(ctx context.Context, tx Tx) error

type API interface {
	WithTransaction(ctx context.Context, fn TransactionFunc, options *TransactionOptions) error
	BeginTransaction(ctx context.Context, options *BeginTransactionOptions) (*Transaction, error)
	AcquireLock(ctx context.Context, namespace, lockKey, transactionID string, mode LockMode, options *AcquireLockOptions) (*LockHolder, error)
	RenewTransaction(ctx context.Context, transactionID string, extra time.Duration) (*Transaction, error)
	ReleaseLock(ctx context.Context, lockID, transactionID string) error
	ReleaseTransactionLocks(ctx context.Context, transactionID string) error
	GetLockStatus(ctx context.Context, namespace, lockKey string) (*LockStatusResponse, error)
	GetTransactionStatus(ctx context.Context, transactionID string) (*TransactionStatusResponse, error)
	Close() error
}

var (
	_ API = (*Client)(nil)
	_ API = (*InMemoryClient)(nil)
)
