package dls

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/caerus-dev/caerus-sdk-go/internal/logging"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

func queuedThenGranted(keepAlives int) te.AcquireHandler {
	return func(_ *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
		for range keepAlives {
			_ = stream.Send(te.WithStatus(dlspb.LockStatus_QUEUED))
			time.Sleep(20 * time.Millisecond)
		}
		return stream.Send(te.Acquired("lock-1", 10))
	}
}

func TestNotifiesOnceEvenWhenTheEngineSendsSeveralKeepAliveQueued(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, queuedThenGranted(4))
	var notified atomic.Int32
	te.Must(connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, &AcquireLockOptions{OnQueued: func() { notified.Add(1) }}))
	te.Equal(t, notified.Load(), int32(1))
}

func TestTheNoticeArrivesBeforeTheGrantNeverAfter(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, queuedThenGranted(1))
	var mu sync.Mutex
	var order []string
	lock := te.Must(connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, &AcquireLockOptions{OnQueued: func() {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "queued")
	}}))
	mu.Lock()
	order = append(order, "granted:"+lock.LockID)
	mu.Unlock()
	te.Equal(t, order, []string{"queued", "granted:lock-1"})
}

func TestDoesNotNotifyWhenTheLockIsGrantedRightAway(t *testing.T) {
	engine := te.StartDLS(t)
	var notified atomic.Int32
	te.Must(connect(t, engine).AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, &AcquireLockOptions{OnQueued: func() { notified.Add(1) }}))
	te.Equal(t, notified.Load(), int32(0))
}

func TestIfTheCallbackPanicsTheAcquisitionCarriesOnAndItIsLogged(t *testing.T) {
	engine := te.StartDLS(t)
	te.OnAcquire(engine, queuedThenGranted(2))
	var out bytes.Buffer
	client := connect(t, engine, func(o *ClientOptions) { o.Logger = logging.NewTo("[caerus-dls]", &out) })
	lock := te.Must(client.AcquireLock(ctx, "ns", "k", "tx-1", Exclusive, &AcquireLockOptions{OnQueued: func() { panic("explote") }}))
	te.Equal(t, lock.LockID, "lock-1")
	te.Contains(t, out.String(), "onQueued callback threw panic=explote")
}

func TestWorksTheSameInsideWithTransaction(t *testing.T) {
	engine, _ := scripted(t)
	te.OnAcquire(engine, queuedThenGranted(3))
	var notified atomic.Int32
	te.NoError(t, connect(t, engine).WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		_, err := tx.AcquireLock(ctx, "ns", "k", Exclusive, &AcquireLockOptions{OnQueued: func() { notified.Add(1) }})
		return err
	}, noRenew))
	te.Equal(t, notified.Load(), int32(1))
}

func TestTheInMemoryClientAcceptsTheOptionWithoutCallingIt(t *testing.T) {
	mock := NewInMemoryClient()
	var notified atomic.Int32
	te.NoError(t, mock.WithTransaction(ctx, func(ctx context.Context, tx Tx) error {
		_, err := tx.AcquireLock(ctx, "ns", "k", Exclusive, &AcquireLockOptions{OnQueued: func() { notified.Add(1) }})
		return err
	}, noRenew))
	te.Equal(t, notified.Load(), int32(0))
}
