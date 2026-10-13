package sre

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
	"github.com/caerus-dev/caerus-sdk-go/internal/transport"
)

func failingTake(t *testing.T, err error) error {
	t.Helper()
	engine := te.StartSRE(t)
	te.OnSRE(engine, "take", func(ctx context.Context, _ *srepb.TakeRequest) (*srepb.ResourceHolderResponse, error) {
		return nil, err
	})
	_, got := connect(t, engine).Pooled("seat").Take(context.Background(), nil)
	return got
}

func TestPullsTheReasonOutOfAGRPCError(t *testing.T) {
	st, _ := status.FromError(te.WithReason(codes.FailedPrecondition, "x", "OUT_OF_STOCK"))
	te.Equal(t, transport.ReasonOf(st), "OUT_OF_STOCK")
}

func TestSaysNothingWhenThereIsNoTrailer(t *testing.T) {
	st, _ := status.FromError(te.Status(codes.FailedPrecondition, "x"))
	te.Equal(t, transport.ReasonOf(st), "")
}

func TestSurvivesAnErrorWithNoMetadataAtAll(t *testing.T) {
	failure := transport.Describe(errors.New("plain"), nil, "req_client_1", "fallback")
	te.Equal(t, failure.Reason, "")
	te.Equal(t, failure.RequestID, "req_client_1")
	te.Equal(t, failure.IsStatus, false)
}

func TestIgnoresDetailsThatAreNotErrorInfo(t *testing.T) {
	st, err := status.New(codes.FailedPrecondition, "x").WithDetails(&errdetails.DebugInfo{Detail: "OUT_OF_STOCK"})
	te.NoError(t, err)
	te.Equal(t, transport.ReasonOf(st), "")
}

func TestTurnsEachReasonIntoItsOwnType(t *testing.T) {
	cases := map[string]func(error){
		"OUT_OF_STOCK":                 func(err error) { te.As[*OutOfStockError](t, err) },
		"HOLDER_NOT_ACTIVE":            func(err error) { te.As[*HolderNotActiveError](t, err) },
		"RESOURCE_HAS_ACTIVE_HOLDS":    func(err error) { te.As[*ResourceHasActiveHoldsError](t, err) },
		"RESOURCE_HAS_QUEUED_REQUESTS": func(err error) { te.As[*ResourceHasQueuedRequestsError](t, err) },
	}
	for reason, check := range cases {
		err := failingTake(t, te.WithReason(codes.FailedPrecondition, "conflict", reason))
		check(err)
		conflict := te.As[*ConflictError](t, err)
		te.Equal(t, conflict.Code(), caerus.CodeConflict)
		te.Equal(t, conflict.Reason(), reason)
		te.As[*caerus.Error](t, err)
	}
}

func TestTheSubtypesDoNotMatchEachOther(t *testing.T) {
	err := failingTake(t, te.WithReason(codes.FailedPrecondition, "conflict", "OUT_OF_STOCK"))
	te.NotAs[*HolderNotActiveError](t, err)
	te.NotAs[*ResourceHasActiveHoldsError](t, err)
	te.NotAs[*ValidationError](t, err)
}

func TestFallsBackToAPlainConflictErrorWhenTheReasonIsUnknown(t *testing.T) {
	err := failingTake(t, te.WithReason(codes.FailedPrecondition, "conflict", "SOMETHING_NEW"))
	conflict := te.As[*ConflictError](t, err)
	te.Equal(t, conflict.Reason(), "SOMETHING_NEW")
	te.NotAs[*OutOfStockError](t, err)
}

func TestFallsBackWhenTheEngineSendsNoReasonAtAll(t *testing.T) {
	err := failingTake(t, te.Status(codes.FailedPrecondition, "conflict"))
	conflict := te.As[*ConflictError](t, err)
	te.Equal(t, conflict.Reason(), "")
}

func TestCarriesTheReasonOnStatusesThatHaveNoSubtype(t *testing.T) {
	err := failingTake(t, te.WithReason(codes.InvalidArgument, "bad", "IDEMPOTENCY_KEY_REQUIRED"))
	validation := te.As[*ValidationError](t, err)
	te.Equal(t, validation.Reason(), "IDEMPOTENCY_KEY_REQUIRED")
	te.Equal(t, validation.Code(), caerus.CodeValidation)
}

func TestMapsEveryStatusTheEngineUses(t *testing.T) {
	check := func(code codes.Code, want caerus.Code, typed func(error)) {
		err := failingTake(t, te.Status(code, "msg"))
		typed(err)
		te.Equal(t, te.As[*caerus.Error](t, err).Code(), want)
	}
	check(codes.NotFound, caerus.CodeResourceNotFound, func(err error) { te.As[*ResourceNotFoundError](t, err) })
	check(codes.InvalidArgument, caerus.CodeValidation, func(err error) { te.As[*ValidationError](t, err) })
	check(codes.Unauthenticated, caerus.CodeAuthentication, func(err error) { te.As[*AuthenticationError](t, err) })
	check(codes.Unavailable, caerus.CodeUnknown, func(err error) { te.NotAs[*ConflictError](t, err) })
}

func TestKeepsTheMessageTheEngineWrote(t *testing.T) {
	err := failingTake(t, te.WithReason(codes.FailedPrecondition, "Out of stock for resource: seat", "OUT_OF_STOCK"))
	te.Contains(t, err.Error(), "Out of stock for resource: seat")
}

func TestDecodesTheTrailersCapturedFromTheRealEngine(t *testing.T) {
	err := failingTake(t, te.FromCapturedTrailer(te.CapturedSRE["OUT_OF_STOCK"]))
	te.As[*OutOfStockError](t, err)
	te.Contains(t, err.Error(), "Out of stock for resource: funcionhorizonte_E8")
	err = failingTake(t, te.FromCapturedTrailer(te.CapturedSRE["HOLDER_NOT_ACTIVE"]))
	te.As[*HolderNotActiveError](t, err)
	err = failingTake(t, te.FromCapturedTrailer(te.CapturedSRE["RESOURCE_HAS_ACTIVE_HOLDS"]))
	te.As[*ResourceHasActiveHoldsError](t, err)
	err = failingTake(t, te.FromCapturedTrailer(te.CapturedSRE["RESOURCE_NOT_FOUND"]))
	te.Equal(t, te.As[*ResourceNotFoundError](t, err).Reason(), "RESOURCE_NOT_FOUND")
	err = failingTake(t, te.FromCapturedTrailer(te.CapturedSRE["TEMPLATE_NOT_FOUND"]))
	te.Equal(t, te.As[*ResourceNotFoundError](t, err).Reason(), "TEMPLATE_NOT_FOUND")
}

func TestPropagatesTheServerRequestID(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "take", func(ctx context.Context, _ *srepb.TakeRequest) (*srepb.ResourceHolderResponse, error) {
		te.SetRequestID(ctx, "req_from_server_1")
		return nil, te.Status(codes.NotFound, "gone")
	})
	_, err := connect(t, engine).Pooled("seat").Take(context.Background(), nil)
	base := te.As[*caerus.Error](t, err)
	te.Equal(t, base.RequestID(), "req_from_server_1")
	te.Contains(t, base.Error(), "(Request ID: req_from_server_1). Contacta soporte en Discord con este ID.")
}

func TestFallsBackToTheClientRequestIDWhenTheServerSentNone(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "take", func(ctx context.Context, _ *srepb.TakeRequest) (*srepb.ResourceHolderResponse, error) {
		return nil, te.Status(codes.NotFound, "gone")
	})
	_, err := connect(t, engine).Pooled("seat").Take(context.Background(), nil)
	base := te.As[*caerus.Error](t, err)
	te.Equal(t, base.RequestID(), engine.LastMetadata().Get("x-request-id")[0])
}

func TestTakesTheFirstRequestIDWhenTheServerSentSeveral(t *testing.T) {
	failure := transport.Describe(te.Status(codes.NotFound, "x"), metadata.Pairs("x-request-id", "req_first_111", "x-request-id", "req_second_22"), "req_client", "f")
	te.Equal(t, failure.RequestID, "req_first_111")
}

func TestANonGRPCFailureIsTheBaseErrorWithItsOwnMessage(t *testing.T) {
	err := fromFailure(transport.Describe(errors.New("socket closed"), nil, "", "Caerus call failed"))
	base := te.As[*caerus.Error](t, err)
	te.Equal(t, base.Code(), caerus.CodeUnknown)
	te.Equal(t, base.Error(), "socket closed")
}
