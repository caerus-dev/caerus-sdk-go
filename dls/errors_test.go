package dls

import (
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
	"github.com/caerus-dev/caerus-sdk-go/internal/transport"
)

func failingAcquire(t *testing.T, err error) error {
	t.Helper()
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(*dlspb.AcquireLockRequest, grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		return err
	})
	_, got := connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil)
	return got
}

func TestARealDeadlockAbortArrivesTypedAndWithItsReason(t *testing.T) {
	err := failingAcquire(t, te.FromCapturedTrailer(te.CapturedDeadlock))
	deadlock := te.As[*DeadlockAbortedError](t, err)
	te.Equal(t, deadlock.Reason(), caerus.ReasonDeadlockDetected)
	te.Equal(t, deadlock.Code(), caerus.CodeConflict)
	te.Contains(t, deadlock.Error(), "marked ABORT_REQUESTED")
	te.As[*ConflictError](t, err)
}

func TestTheReasonWinsOverTheStatus(t *testing.T) {
	cases := []struct {
		code   codes.Code
		reason string
		check  func(error)
	}{
		{codes.Aborted, "DEADLOCK_DETECTED", func(err error) { te.As[*DeadlockAbortedError](t, err) }},
		{codes.FailedPrecondition, "LOCK_DENIED", func(err error) { te.As[*LockDeniedError](t, err) }},
		{codes.AlreadyExists, "LOCK_ALREADY_HELD_EXCLUSIVELY", func(err error) { te.As[*LockAlreadyHeldError](t, err) }},
		{codes.FailedPrecondition, "TRANSACTION_NOT_ACTIVE", func(err error) { te.As[*TransactionNotActiveError](t, err) }},
		{codes.InvalidArgument, "LOCK_MODE_MISMATCH", func(err error) {
			te.As[*LockModeMismatchError](t, err)
			te.As[*ValidationError](t, err)
		}},
		{codes.Unknown, "LOCK_ACQUISITION_CANCELLED", func(err error) {
			cancelled := te.As[*LockAcquisitionCancelledError](t, err)
			te.Equal(t, cancelled.Code(), caerus.CodeUnknown)
		}},
	}
	for _, c := range cases {
		err := failingAcquire(t, te.WithReason(c.code, "msg", c.reason))
		c.check(err)
		te.Equal(t, te.As[*Error](t, err).Reason(), c.reason)
	}
}

func TestMapsTheStatusWhenThereIsNoKnownReason(t *testing.T) {
	cases := []struct {
		code  codes.Code
		want  caerus.Code
		check func(error)
	}{
		{codes.NotFound, caerus.CodeResourceNotFound, func(err error) { te.As[*NotFoundError](t, err) }},
		{codes.AlreadyExists, caerus.CodeConflict, func(err error) { te.As[*LockAlreadyHeldError](t, err) }},
		{codes.Aborted, caerus.CodeConflict, func(err error) {
			te.As[*ConflictError](t, err)
			te.NotAs[*DeadlockAbortedError](t, err)
		}},
		{codes.FailedPrecondition, caerus.CodeConflict, func(err error) { te.As[*ConflictError](t, err) }},
		{codes.InvalidArgument, caerus.CodeValidation, func(err error) { te.As[*ValidationError](t, err) }},
		{codes.Unauthenticated, caerus.CodeAuthentication, func(err error) { te.As[*AuthenticationError](t, err) }},
		{codes.Canceled, caerus.CodeUnknown, func(err error) { te.As[*LockAcquisitionCancelledError](t, err) }},
		{codes.Internal, caerus.CodeUnknown, func(err error) { te.NotAs[*ConflictError](t, err) }},
	}
	for _, c := range cases {
		err := failingAcquire(t, te.Status(c.code, "msg"))
		c.check(err)
		te.Equal(t, te.As[*caerus.Error](t, err).Code(), c.want)
	}
}

func TestEveryDLSErrorCanBeCaughtAsADLSErrorAndAsACaerusError(t *testing.T) {
	all := []error{
		NewError("x", caerus.CodeUnknown, nil), NewNotFoundError("x", nil), NewConflictError("x", nil),
		NewValidationError("x", nil), NewAuthenticationError("x", nil), NewTimeoutError("x", nil),
		NewDeadlockAbortedError("x", nil), NewLockDeniedError("x", nil), NewLockAlreadyHeldError("x", nil),
		NewTransactionNotActiveError("x", nil), NewLockModeMismatchError("x", nil), NewLockAcquisitionCancelledError("x", nil),
	}
	for _, err := range all {
		te.As[*Error](t, err)
		te.As[*caerus.Error](t, err)
	}
}

func TestPropagatesTheServerRequestIDOrFallsBackToTheClientOne(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		stream.SetTrailer(map[string][]string{"x-request-id": {"req_server_stream_1"}})
		return te.Status(codes.NotFound, "x")
	})
	_, err := connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil)
	te.Equal(t, te.As[*Error](t, err).RequestID(), "req_server_stream_1")
	engine.Reset()
	te.OnAcquire(engine, func(*dlspb.AcquireLockRequest, grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		return te.Status(codes.NotFound, "x")
	})
	_, err = connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, nil)
	te.Equal(t, te.As[*Error](t, err).RequestID(), engine.LastMetadata().Get("x-request-id")[0])
}

func TestIsGoneOnlyForTheErrorsThatEndATransaction(t *testing.T) {
	te.True(t, isGone(NewNotFoundError("x", nil)), "not found")
	te.True(t, isGone(NewTransactionNotActiveError("x", nil)), "not active")
	te.True(t, isGone(NewDeadlockAbortedError("x", nil)), "deadlock")
	te.True(t, !isGone(NewConflictError("x", nil)), "conflict")
	te.True(t, !isGone(NewTimeoutError("x", nil)), "timeout")
	te.True(t, !isGone(errors.New("x")), "plain")
}

func TestANonGRPCFailureIsTheBaseDLSError(t *testing.T) {
	err := fromFailure(transport.Describe(errors.New("socket closed"), nil, "", fallbackMessage))
	dlsErr := te.As[*Error](t, err)
	te.Equal(t, dlsErr.Code(), caerus.CodeUnknown)
	te.Equal(t, dlsErr.Error(), "socket closed")
}
