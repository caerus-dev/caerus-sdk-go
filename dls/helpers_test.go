package dls

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/config"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

var ctx = context.Background()

func noEnv(string) (string, bool) {
	return "", false
}

func env(values map[string]string) config.Environment {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func connect(t *testing.T, engine *te.DLS, options ...func(*ClientOptions)) *Client {
	t.Helper()
	resolved := ClientOptions{APIKey: "sk_test_123", Endpoint: engine.Endpoint(), TLS: caerus.Bool(false)}
	for _, option := range options {
		option(&resolved)
	}
	client, err := newClient(resolved, noEnv)
	te.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

type journal struct {
	mu    sync.Mutex
	calls []string
}

func (j *journal) add(call string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, call)
}

func (j *journal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.calls)
}

func (j *journal) count(call string) int {
	total := 0
	for _, seen := range j.list() {
		if seen == call {
			total++
		}
	}
	return total
}

func scripted(t *testing.T) (*te.DLS, *journal) {
	engine := te.StartDLS(t)
	calls := &journal{}
	te.OnDLS(engine, "beginTransaction", func(context.Context, *dlspb.BeginTransactionRequest) (*dlspb.BeginTransactionResponse, error) {
		calls.add("begin")
		return &dlspb.BeginTransactionResponse{TransactionId: "tx-1"}, nil
	})
	te.OnDLS(engine, "releaseTransactionLocks", func(context.Context, *dlspb.ReleaseTransactionLocksRequest) (*emptypb.Empty, error) {
		calls.add("releaseAll")
		return &emptypb.Empty{}, nil
	})
	te.OnDLS(engine, "renewTransaction", func(context.Context, *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
		calls.add("renew")
		return &dlspb.RenewTransactionResponse{TransactionId: "tx-1"}, nil
	})
	te.OnAcquire(engine, func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		return stream.Send(te.Acquired("lock-1", 7))
	})
	return engine, calls
}

func queuedForever(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
	if err := stream.Send(te.WithStatus(dlspb.LockStatus_QUEUED)); err != nil {
		return err
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

var noRenew = &TransactionOptions{DisableAutoRenew: true}

func life(timeout time.Duration) *TransactionOptions {
	return &TransactionOptions{Timeout: timeout}
}
