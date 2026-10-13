package dls

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/logging"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

func TestBeginTransactionPassesTheTimeoutInMilliseconds(t *testing.T) {
	engine := te.StartDLS(t)
	client := connect(t, engine)
	transaction := te.Must(client.BeginTransaction(ctx, &BeginTransactionOptions{Timeout: 5 * time.Second}))
	te.Equal(t, transaction.TransactionID, "tx-1")
	te.Equal(t, engine.LastRequest().(*dlspb.BeginTransactionRequest).GetTimeoutMs(), int64(5000))
	te.Must(client.BeginTransaction(ctx, nil))
	te.True(t, engine.LastRequest().(*dlspb.BeginTransactionRequest).TimeoutMs == nil, "timeout sent")
}

func TestSendsTheAPIKeyAndARequestIDOnEveryCall(t *testing.T) {
	engine := te.StartDLS(t)
	client := connect(t, engine)
	check := func() {
		te.Equal(t, engine.LastMetadata().Get("authorization"), []string{"Bearer sk_test_123"})
		te.True(t, regexp.MustCompile(`^req_[a-zA-Z0-9_\-]{8,64}$`).MatchString(engine.LastMetadata().Get("x-request-id")[0]), "bad request id")
	}
	te.Must(client.BeginTransaction(ctx, nil))
	check()
	te.Must(client.AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil))
	check()
	te.Must(client.GetLockStatus(ctx, "ns", "k"))
	check()
}

func TestAcquireLockMapsTheModeAndPassesItsArguments(t *testing.T) {
	engine := te.StartDLS(t)
	client := connect(t, engine)
	lock := te.Must(client.AcquireLock(ctx, "cuentas", "cuenta-42", "tx-1", SharedRead, &AcquireLockOptions{IdempotencyKey: "idem-1"}))
	request := engine.LastRequest().(*dlspb.AcquireLockRequest)
	te.Equal(t, request.GetNamespace(), "cuentas")
	te.Equal(t, request.GetLockKey(), "cuenta-42")
	te.Equal(t, request.GetTransactionId(), "tx-1")
	te.Equal(t, request.GetRequestedMode(), dlspb.LockMode_SHARED_READ)
	te.Equal(t, request.GetIdempotencyKey(), "idem-1")
	te.Equal(t, *lock, LockHolder{LockID: "lock-1", FencingToken: 1234, Status: Acquired})
	te.Must(client.AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil))
	request = engine.LastRequest().(*dlspb.AcquireLockRequest)
	te.Equal(t, request.GetRequestedMode(), dlspb.LockMode_EXCLUSIVE)
	te.True(t, request.IdempotencyKey == nil, "idempotency key sent")
}

func TestAcquireLockReturnsLockDeniedErrorWhenTheEngineDeniesTheLock(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		return stream.Send(te.WithStatus(dlspb.LockStatus_DENIED))
	})
	_, err := connect(t, engine).AcquireLock(ctx, "cuentas", "c-1", "tx-1", Exclusive, nil)
	denied := te.As[*LockDeniedError](t, err)
	te.Equal(t, denied.Reason(), caerus.ReasonLockDenied)
	te.Equal(t, denied.Code(), caerus.CodeConflict)
	te.Contains(t, denied.Error(), "Caerus denied the lock on cuentas/c-1")
	te.As[*ConflictError](t, err)
	te.As[*Error](t, err)
	te.As[*caerus.Error](t, err)
}

func TestAnUnspecifiedStatusInTheStreamIsLoggedAndIgnored(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		_ = stream.Send(te.WithStatus(dlspb.LockStatus_LOCK_STATUS_UNSPECIFIED))
		return stream.Send(te.Acquired("lock-2", 3))
	})
	var out bytes.Buffer
	client := connect(t, engine, func(o *ClientOptions) { o.Logger = logging.NewTo("[caerus-dls]", &out) })
	lock := te.Must(client.AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil))
	te.Equal(t, lock.LockID, "lock-2")
	te.Contains(t, out.String(), "[caerus-dls] Received unspecified lock status in stream")
}

func TestAFencingTokenOfZeroComesBackAsZeroNeverNegative(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		return stream.Send(te.Acquired("lock-3", -5))
	})
	te.Equal(t, te.Must(connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil)).FencingToken, int64(0))
}

func TestAFencingTokenBeyondTheIntRangePassesAsIs(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		return stream.Send(te.Acquired("lock-4", 9_007_199_254_740_993))
	})
	te.Equal(t, te.Must(connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil)).FencingToken, int64(9_007_199_254_740_993))
}

func TestRenewTransactionPassesItsArguments(t *testing.T) {
	engine := te.StartDLS(t)
	transaction := te.Must(connect(t, engine).RenewTransaction(ctx, "tx-1", 1500*time.Millisecond))
	request := engine.LastRequest().(*dlspb.RenewTransactionRequest)
	te.Equal(t, request.GetTransactionId(), "tx-1")
	te.Equal(t, request.GetExtraMs(), int64(1500))
	te.Equal(t, transaction.TransactionID, "tx-1")
}

func TestReleaseCallsPassTheirArguments(t *testing.T) {
	engine := te.StartDLS(t)
	client := connect(t, engine)
	te.NoError(t, client.ReleaseLock(ctx, "lock-1", "tx-1"))
	release := engine.LastRequest().(*dlspb.ReleaseLockRequest)
	te.Equal(t, release.GetLockId(), "lock-1")
	te.Equal(t, release.GetTransactionId(), "tx-1")
	te.NoError(t, client.ReleaseTransactionLocks(ctx, "tx-9"))
	te.Equal(t, engine.LastRequest().(*dlspb.ReleaseTransactionLocksRequest).GetTransactionId(), "tx-9")
}

func TestGetLockStatusMapsTheResponse(t *testing.T) {
	engine := te.StartDLS(t)
	status := te.Must(connect(t, engine).GetLockStatus(ctx, "ns", "k"))
	request := engine.LastRequest().(*dlspb.GetLockStatusRequest)
	te.Equal(t, request.GetNamespace(), "ns")
	te.Equal(t, request.GetLockKey(), "k")
	te.Equal(t, *status, LockStatusResponse{
		IsHeld:        true,
		CurrentMode:   Exclusive,
		ActiveHolders: []ActiveLockHolder{{LockID: "l-1", ExpiresAt: 1000, FencingToken: 5}},
	})
}

func TestGetLockStatusLeavesTheModeEmptyWhenNothingIsHeld(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnDLS(engine, "getLockStatus", func(context.Context, *dlspb.GetLockStatusRequest) (*dlspb.GetLockStatusResponse, error) {
		return &dlspb.GetLockStatusResponse{PendingQueueSize: 3}, nil
	})
	status := te.Must(connect(t, engine).GetLockStatus(ctx, "ns", "k"))
	te.Equal(t, status.CurrentMode, LockMode(""))
	te.Equal(t, status.PendingQueueSize, 3)
	te.Equal(t, len(status.ActiveHolders), 0)
}

func TestGetTransactionStatusMapsTheResponse(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnDLS(engine, "getTransactionStatus", func(context.Context, *dlspb.GetTransactionStatusRequest) (*dlspb.GetTransactionStatusResponse, error) {
		return &dlspb.GetTransactionStatusResponse{
			Status:      "ABORT_REQUESTED",
			AbortReason: "DEADLOCK_VICTIM",
			ExpiresAt:   99,
			Locks: []*dlspb.TransactionLockInfo{
				{Namespace: "ns", LockKey: "a", RequestedMode: dlspb.LockMode_SHARED_READ, Status: dlspb.LockStatus_ACQUIRED},
				{Namespace: "ns", LockKey: "b", RequestedMode: dlspb.LockMode_MODE_UNSPECIFIED, Status: dlspb.LockStatus_QUEUED},
			},
		}, nil
	})
	status := te.Must(connect(t, engine).GetTransactionStatus(ctx, "tx-1"))
	te.Equal(t, engine.LastRequest().(*dlspb.GetTransactionStatusRequest).GetTransactionId(), "tx-1")
	te.Equal(t, *status, TransactionStatusResponse{
		Status:      "ABORT_REQUESTED",
		AbortReason: "DEADLOCK_VICTIM",
		ExpiresAt:   99,
		Locks: []TransactionLockInfo{
			{Namespace: "ns", LockKey: "a", RequestedMode: SharedRead, Status: Acquired},
			{Namespace: "ns", LockKey: "b", RequestedMode: Exclusive, Status: Queued},
		},
	})
}

func TestAnEmptyAbortReasonReadsAsNone(t *testing.T) {
	te.Equal(t, te.Must(connect(t, te.StartDLS(t)).GetTransactionStatus(ctx, "tx-1")).AbortReason, "")
}

func TestTranslatesAServerErrorWithItsRequestID(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnDLS(engine, "getTransactionStatus", func(ctx context.Context, _ *dlspb.GetTransactionStatusRequest) (*dlspb.GetTransactionStatusResponse, error) {
		te.SetRequestID(ctx, "req_server_side_1")
		return nil, te.Status(codes.NotFound, "Transaction not found")
	})
	_, err := connect(t, engine).GetTransactionStatus(ctx, "tx-x")
	notFound := te.As[*NotFoundError](t, err)
	te.Equal(t, notFound.RequestID(), "req_server_side_1")
	te.Equal(t, notFound.Code(), caerus.CodeResourceNotFound)
	te.True(t, strings.HasSuffix(notFound.Error(), "(Request ID: req_server_side_1). Contacta soporte en Discord con este ID."), "message %q", notFound.Error())
}
