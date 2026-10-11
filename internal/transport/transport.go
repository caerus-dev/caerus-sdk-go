package transport

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caerus-dev/caerus-sdk-go/internal/config"
)

const (
	AuthorizationHeader = "authorization"
	RequestIDHeader     = "x-request-id"
	errorInfoSuffix     = "google.rpc.ErrorInfo"
)

type Conn struct {
	cc      *grpc.ClientConn
	apiKey  string
	timeout time.Duration
	closed  atomic.Bool
}

func Dial(settings config.Settings, extra ...grpc.DialOption) (*Conn, error) {
	var creds credentials.TransportCredentials
	if settings.TLS {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	} else {
		creds = insecure.NewCredentials()
	}
	options := append([]grpc.DialOption{grpc.WithTransportCredentials(creds)}, extra...)
	cc, err := grpc.NewClient(settings.Endpoint, options...)
	if err != nil {
		return nil, err
	}
	return &Conn{cc: cc, apiKey: settings.APIKey, timeout: settings.Timeout}, nil
}

func (c *Conn) ClientConn() grpc.ClientConnInterface {
	return c.cc
}

func (c *Conn) Timeout() time.Duration {
	return c.timeout
}

func (c *Conn) Closed() bool {
	return c.closed.Load()
}

func (c *Conn) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	return c.cc.Close()
}

func (c *Conn) Outgoing(ctx context.Context, requestID string) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		AuthorizationHeader, "Bearer "+c.apiKey,
		RequestIDHeader, requestID)
}

func NewRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("req_%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type Failure struct {
	Cause     error
	IsStatus  bool
	Code      codes.Code
	Message   string
	Reason    string
	RequestID string
}

func Describe(err error, trailer metadata.MD, clientRequestID, fallback string) Failure {
	failure := Failure{Cause: err, Code: codes.Unknown, RequestID: clientRequestID}
	if ids := trailer.Get(RequestIDHeader); len(ids) > 0 && ids[0] != "" {
		failure.RequestID = ids[0]
	}
	st, ok := status.FromError(err)
	if ok && st != nil {
		failure.IsStatus = true
		failure.Code = st.Code()
		failure.Message = st.Message()
		failure.Reason = ReasonOf(st)
	}
	if failure.Message == "" && err != nil && !failure.IsStatus {
		failure.Message = err.Error()
	}
	if failure.Message == "" {
		failure.Message = fallback
	}
	return failure
}

func ReasonOf(st *status.Status) (reason string) {
	defer func() {
		if recover() != nil {
			reason = ""
		}
	}()
	for _, detail := range st.Proto().GetDetails() {
		if !strings.HasSuffix(detail.GetTypeUrl(), errorInfoSuffix) {
			continue
		}
		var info errdetails.ErrorInfo
		if proto.Unmarshal(detail.GetValue(), &info) != nil {
			continue
		}
		if info.GetReason() != "" {
			return info.GetReason()
		}
	}
	return ""
}
