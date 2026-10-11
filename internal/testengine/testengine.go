package testengine

import (
	"context"
	"encoding/base64"
	"net"
	"sync"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb"
)

type recorder struct {
	mu       sync.Mutex
	handlers map[string]any
	metadata metadata.MD
	request  proto.Message
	method   string
	calls    map[string]int
	server   *grpc.Server
	listener net.Listener
}

func (r *recorder) start(t testing.TB, register func(*grpc.Server)) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	r.handlers = map[string]any{}
	r.calls = map[string]int{}
	r.listener = listener
	r.server = grpc.NewServer(
		grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			r.capture(ctx)
			return handler(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			r.capture(stream.Context())
			return handler(srv, stream)
		}),
	)
	register(r.server)
	go func() { _ = r.server.Serve(listener) }()
	t.Cleanup(r.server.Stop)
}

func (r *recorder) capture(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metadata = md.Copy()
}

func (r *recorder) Endpoint() string {
	return r.listener.Addr().String()
}

func (r *recorder) LastMetadata() metadata.MD {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.metadata
}

func (r *recorder) LastRequest() proto.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.request
}

func (r *recorder) LastMethod() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.method
}

func (r *recorder) Calls(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[method]
}

func (r *recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = map[string]any{}
	r.calls = map[string]int{}
	r.metadata = nil
	r.request = nil
	r.method = ""
}

func (r *recorder) set(method string, handler any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[method] = handler
}

func (r *recorder) record(method string, request proto.Message) any {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.request = request
	r.method = method
	r.calls[method]++
	return r.handlers[method]
}

type Unary[Req, Res any] func(ctx context.Context, request Req) (Res, error)

func dispatch[Req proto.Message, Res any](r *recorder, ctx context.Context, method string, request Req, fallback func() Res) (Res, error) {
	if handler, ok := r.record(method, request).(Unary[Req, Res]); ok {
		return handler(ctx, request)
	}
	return fallback(), nil
}

type SRE struct {
	srepb.UnimplementedSharedResourceEngineServer
	recorder
}

func StartSRE(t testing.TB) *SRE {
	engine := &SRE{}
	engine.start(t, func(server *grpc.Server) { srepb.RegisterSharedResourceEngineServer(server, engine) })
	return engine
}

func OnSRE[Req, Res any](engine *SRE, method string, handler Unary[Req, Res]) {
	engine.set(method, handler)
}

func AResource() *srepb.ResourceResponse {
	return &srepb.ResourceResponse{ResourceId: "res-1", Key: "seat_A12", TemplateId: "tpl-1", AvailableAmount: 1}
}

func AHolder() *srepb.ResourceHolderResponse {
	return &srepb.ResourceHolderResponse{
		HolderId:   "hld-1",
		ResourceId: "res-1",
		Status:     srepb.ResourceHolderResponse_PENDING,
		Amount:     1,
		ExpiresAt:  1_785_164_400,
	}
}

func (e *SRE) CreateResource(ctx context.Context, request *srepb.CreateResourceRequest) (*srepb.ResourceResponse, error) {
	return dispatch(&e.recorder, ctx, "createResource", request, AResource)
}

func (e *SRE) UpdateResource(ctx context.Context, request *srepb.UpdateResourceRequest) (*srepb.ResourceResponse, error) {
	return dispatch(&e.recorder, ctx, "updateResource", request, AResource)
}

func (e *SRE) DeleteResource(ctx context.Context, request *srepb.DeleteResourceRequest) (*emptypb.Empty, error) {
	return dispatch(&e.recorder, ctx, "deleteResource", request, func() *emptypb.Empty { return &emptypb.Empty{} })
}

func (e *SRE) Take(ctx context.Context, request *srepb.TakeRequest) (*srepb.ResourceHolderResponse, error) {
	return dispatch(&e.recorder, ctx, "take", request, AHolder)
}

func (e *SRE) Confirm(ctx context.Context, request *srepb.ConfirmRequest) (*srepb.ResourceHolderResponse, error) {
	return dispatch(&e.recorder, ctx, "confirm", request, func() *srepb.ResourceHolderResponse {
		holder := AHolder()
		holder.Status = srepb.ResourceHolderResponse_CONFIRMED
		return holder
	})
}

func (e *SRE) Release(ctx context.Context, request *srepb.ReleaseRequest) (*emptypb.Empty, error) {
	return dispatch(&e.recorder, ctx, "release", request, func() *emptypb.Empty { return &emptypb.Empty{} })
}

func (e *SRE) Extend(ctx context.Context, request *srepb.ExtendRequest) (*srepb.ResourceHolderResponse, error) {
	return dispatch(&e.recorder, ctx, "extend", request, AHolder)
}

func (e *SRE) GetResource(ctx context.Context, request *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
	return dispatch(&e.recorder, ctx, "getResource", request, AResource)
}

func (e *SRE) GetResourcesByGroupKey(ctx context.Context, request *srepb.GetResourcesByGroupKeyRequest) (*srepb.GetResourcesByGroupKeyResponse, error) {
	return dispatch(&e.recorder, ctx, "getResourcesByGroupKey", request, func() *srepb.GetResourcesByGroupKeyResponse {
		return &srepb.GetResourcesByGroupKeyResponse{Resources: []*srepb.ResourceResponse{AResource()}}
	})
}

func (e *SRE) GetResourceHolder(ctx context.Context, request *srepb.GetResourceHolderRequest) (*srepb.ResourceHolderResponse, error) {
	return dispatch(&e.recorder, ctx, "getResourceHolder", request, AHolder)
}

func (e *SRE) GetResourceHoldersList(ctx context.Context, request *srepb.GetResourceHoldersListRequest) (*srepb.GetResourceHoldersListResponse, error) {
	return dispatch(&e.recorder, ctx, "getResourceHoldersList", request, func() *srepb.GetResourceHoldersListResponse {
		return &srepb.GetResourceHoldersListResponse{ResourceHolders: []*srepb.ResourceHolderResponse{AHolder()}}
	})
}

type DLS struct {
	dlspb.UnimplementedDistributedLockingEngineServer
	recorder
}

type AcquireHandler func(request *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error

func StartDLS(t testing.TB) *DLS {
	engine := &DLS{}
	engine.start(t, func(server *grpc.Server) { dlspb.RegisterDistributedLockingEngineServer(server, engine) })
	return engine
}

func OnDLS[Req, Res any](engine *DLS, method string, handler Unary[Req, Res]) {
	engine.set(method, handler)
}

func OnAcquire(engine *DLS, handler AcquireHandler) {
	engine.set("acquireLock", handler)
}

func Acquired(lockID string, fencingToken int64) *dlspb.AcquireLockResponse {
	return &dlspb.AcquireLockResponse{LockId: lockID, FencingToken: fencingToken, Status: dlspb.LockStatus_ACQUIRED}
}

func WithStatus(status dlspb.LockStatus) *dlspb.AcquireLockResponse {
	return &dlspb.AcquireLockResponse{Status: status}
}

func (e *DLS) BeginTransaction(ctx context.Context, request *dlspb.BeginTransactionRequest) (*dlspb.BeginTransactionResponse, error) {
	return dispatch(&e.recorder, ctx, "beginTransaction", request, func() *dlspb.BeginTransactionResponse {
		return &dlspb.BeginTransactionResponse{TransactionId: "tx-1"}
	})
}

func (e *DLS) AcquireLock(request *dlspb.AcquireLockRequest, stream grpc.ServerStreamingServer[dlspb.AcquireLockResponse]) error {
	if handler, ok := e.record("acquireLock", request).(AcquireHandler); ok {
		return handler(request, stream)
	}
	return stream.Send(Acquired("lock-1", 1234))
}

func (e *DLS) RenewTransaction(ctx context.Context, request *dlspb.RenewTransactionRequest) (*dlspb.RenewTransactionResponse, error) {
	return dispatch(&e.recorder, ctx, "renewTransaction", request, func() *dlspb.RenewTransactionResponse {
		return &dlspb.RenewTransactionResponse{TransactionId: "tx-1", NewExpiresAt: 1_234_567_890}
	})
}

func (e *DLS) ReleaseLock(ctx context.Context, request *dlspb.ReleaseLockRequest) (*emptypb.Empty, error) {
	return dispatch(&e.recorder, ctx, "releaseLock", request, func() *emptypb.Empty { return &emptypb.Empty{} })
}

func (e *DLS) ReleaseTransactionLocks(ctx context.Context, request *dlspb.ReleaseTransactionLocksRequest) (*emptypb.Empty, error) {
	return dispatch(&e.recorder, ctx, "releaseTransactionLocks", request, func() *emptypb.Empty { return &emptypb.Empty{} })
}

func (e *DLS) GetLockStatus(ctx context.Context, request *dlspb.GetLockStatusRequest) (*dlspb.GetLockStatusResponse, error) {
	return dispatch(&e.recorder, ctx, "getLockStatus", request, func() *dlspb.GetLockStatusResponse {
		return &dlspb.GetLockStatusResponse{
			IsHeld:        true,
			CurrentMode:   dlspb.LockMode_EXCLUSIVE,
			ActiveHolders: []*dlspb.LockHolderInfo{{LockId: "l-1", ExpiresAt: 1000, FencingToken: 5}},
		}
	})
}

func (e *DLS) GetTransactionStatus(ctx context.Context, request *dlspb.GetTransactionStatusRequest) (*dlspb.GetTransactionStatusResponse, error) {
	return dispatch(&e.recorder, ctx, "getTransactionStatus", request, func() *dlspb.GetTransactionStatusResponse {
		return &dlspb.GetTransactionStatusResponse{Status: "ACTIVE", ExpiresAt: 1000}
	})
}

func Status(code codes.Code, message string) error {
	return status.Error(code, message)
}

func WithReason(code codes.Code, message, reason string) error {
	st, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "caerus.dev"})
	if err != nil {
		panic(err)
	}
	return st.Err()
}

func FromCapturedTrailer(encoded string) error {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		panic(err)
	}
	var decoded spb.Status
	if err := proto.Unmarshal(raw, &decoded); err != nil {
		panic(err)
	}
	return status.FromProto(&decoded).Err()
}

func SetRequestID(ctx context.Context, requestID string) {
	_ = grpc.SetTrailer(ctx, metadata.Pairs("x-request-id", requestID))
}

var CapturedSRE = map[string]string{
	"OUT_OF_STOCK":              "CAkSLk91dCBvZiBzdG9jayBmb3IgcmVzb3VyY2U6IGZ1bmNpb25ob3Jpem9udGVfRTgaRgoodHlwZS5nb29nbGVhcGlzLmNvbS9nb29nbGUucnBjLkVycm9ySW5mbxIaCgxPVVRfT0ZfU1RPQ0sSCmNhZXJ1cy5kZXY=",
	"HOLDER_NOT_ACTIVE":         "CAkSNVJlc291cmNlSG9sZGVyIGlzIG5vdCBpbiBhIHZhbGlkIHN0YXRlIHRvIGJlIGV4dGVuZGVkGksKKHR5cGUuZ29vZ2xlYXBpcy5jb20vZ29vZ2xlLnJwYy5FcnJvckluZm8SHwoRSE9MREVSX05PVF9BQ1RJVkUSCmNhZXJ1cy5kZXY=",
	"RESOURCE_NOT_FOUND":        "CAUSLlJlc291cmNlIG5vdCBmb3VuZCB3aXRoIGlkOiBub19leGlzdGVfamFtYXNfenoaTAoodHlwZS5nb29nbGVhcGlzLmNvbS9nb29nbGUucnBjLkVycm9ySW5mbxIgChJSRVNPVVJDRV9OT1RfRk9VTkQSCmNhZXJ1cy5kZXY=",
	"TEMPLATE_NOT_FOUND":        "CAUSNlRlbXBsYXRlIG5vdCBmb3VuZCB3aXRoIG5hbWU6IHBsYW50aWxsYV9pbmV4aXN0ZW50ZV96ehpMCih0eXBlLmdvb2dsZWFwaXMuY29tL2dvb2dsZS5ycGMuRXJyb3JJbmZvEiAKElRFTVBMQVRFX05PVF9GT1VORBIKY2FlcnVzLmRldg==",
	"RESOURCE_HAS_ACTIVE_HOLDS": "CAkSPUNhbm5vdCBkZWxldGUgcmVzb3VyY2Ugd2l0aCBhY3RpdmUgaG9sZHM6IGZ1bmNpb25ob3Jpem9udGVfRTcaUwoodHlwZS5nb29nbGVhcGlzLmNvbS9nb29nbGUucnBjLkVycm9ySW5mbxInChlSRVNPVVJDRV9IQVNfQUNUSVZFX0hPTERTEgpjYWVydXMuZGV2",
}

const CapturedDeadlock = "CAoSR1RyYW5zYWN0aW9uIGYzZDIzOWY2LWFiNWItNDkzNi05NDc3LThhYzQ5ZDMyZDA2YyBtYXJrZWQgQUJPUlRfUkVRVUVTVEVEGksKKHR5cGUuZ29vZ2xlYXBpcy5jb20vZ29vZ2xlLnJwYy5FcnJvckluZm8SHwoRREVBRExPQ0tfREVURUNURUQSCmNhZXJ1cy5kZXY="
