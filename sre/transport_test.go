package sre

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
	"github.com/caerus-dev/caerus-sdk-go/internal/transport"
)

func TestSendsTheAPIKeyAsABearerTokenInTheMetadata(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	te.Must(client.GetResource(context.Background(), "seat_A12"))
	te.Equal(t, engine.LastMetadata().Get("authorization"), []string{"Bearer sk_test_123"})
}

func TestSendsNoEnvironmentOfItsOwn(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	te.Must(client.GetResource(context.Background(), "seat_A12"))
	te.Equal(t, len(engine.LastMetadata().Get("x-environment-id")), 0)
	te.Equal(t, len(engine.LastMetadata().Get("x-caerus-environment")), 0)
}

func TestSendsARequestIDOnEveryCall(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	pattern := regexp.MustCompile(`^req_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 3 {
		te.Must(client.GetResource(context.Background(), "seat_A12"))
		ids := engine.LastMetadata().Get("x-request-id")
		te.Equal(t, len(ids), 1)
		te.True(t, pattern.MatchString(ids[0]), "malformed request id %q", ids[0])
		te.True(t, regexp.MustCompile(`^req_[a-zA-Z0-9_\-]{8,64}$`).MatchString(ids[0]), "the engine would refuse %q", ids[0])
		seen[ids[0]] = true
	}
	te.Equal(t, len(seen), 3)
}

func TestTurnsANotFoundFromTheServerIntoResourceNotFoundError(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResource", func(ctx context.Context, _ *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
		return nil, te.Status(codes.NotFound, "no such resource")
	})
	_, err := connect(t, engine).GetResource(context.Background(), "nope")
	notFound := te.As[*ResourceNotFoundError](t, err)
	te.Equal(t, notFound.Code(), caerus.CodeResourceNotFound)
	te.Contains(t, notFound.Error(), "no such resource")
}

func TestLeavesAnythingElseAsTheBaseError(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResource", func(ctx context.Context, _ *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
		return nil, te.Status(codes.Internal, "boom")
	})
	_, err := connect(t, engine).GetResource(context.Background(), "x")
	base := te.As[*caerus.Error](t, err)
	te.Equal(t, base.Code(), caerus.CodeUnknown)
	te.NotAs[*ConflictError](t, err)
	te.NotAs[*ResourceNotFoundError](t, err)
}

func TestGivesUpOnACallThatRunsPastItsDeadline(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResource", func(ctx context.Context, _ *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	client := connect(t, engine, func(o *ClientOptions) { o.Timeout = 150 * time.Millisecond })
	started := time.Now()
	_, err := client.GetResource(context.Background(), "x")
	te.As[*TimeoutError](t, err)
	te.True(t, time.Since(started) < 3*time.Second, "took %v", time.Since(started))
}

func TestTheCallerContextAlsoBoundsTheCall(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResource", func(ctx context.Context, _ *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	client := connect(t, engine)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.GetResource(ctx, "x")
	te.As[*TimeoutError](t, err)
}

func TestRejectsCallsMadeAfterItIsClosed(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	te.NoError(t, client.Close())
	_, err := client.GetResource(context.Background(), "x")
	base := te.As[*caerus.Error](t, err)
	te.Contains(t, base.Error(), "has been closed")
	te.Equal(t, engine.Calls("getResource"), 0)
}

func TestClosingTwiceIsHarmless(t *testing.T) {
	client := connect(t, te.StartSRE(t))
	te.NoError(t, client.Close())
	te.NoError(t, client.Close())
}

func TestReturnsTheErrorToTheCallerWithItsCauseReachable(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResource", func(ctx context.Context, _ *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
		return nil, te.Status(codes.NotFound, "gone")
	})
	_, err := connect(t, engine).GetResource(context.Background(), "x")
	base := te.As[*caerus.Error](t, err)
	te.True(t, errors.Unwrap(base) != nil, "the gRPC error should stay reachable")
}

func TestTheRequestIDLooksLikeTheOneTheEngineAccepts(t *testing.T) {
	te.True(t, regexp.MustCompile(`^req_[a-zA-Z0-9_\-]{8,64}$`).MatchString(transport.NewRequestID()), "bad id")
}
