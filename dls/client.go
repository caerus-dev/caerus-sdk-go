package dls

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/config"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
	"github.com/caerus-dev/caerus-sdk-go/internal/transport"
)

const fallbackMessage = "DLS call failed"

type Client struct {
	conn     *transport.Conn
	engine   dlspb.DistributedLockingEngineClient
	endpoint string
	log      *slog.Logger
}

func NewClient(options ClientOptions) (*Client, error) {
	return newClient(options, config.System)
}

func newClient(options ClientOptions, env config.Environment, dial ...grpc.DialOption) (*Client, error) {
	settings, err := config.Resolve(config.DLS, config.Input{
		APIKey:   options.APIKey,
		Endpoint: options.Endpoint,
		TLS:      options.TLS,
		Timeout:  options.Timeout,
		Logger:   options.Logger,
	}, env)
	if err != nil {
		return nil, err
	}
	conn, err := transport.Dial(settings, dial...)
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:     conn,
		engine:   dlspb.NewDistributedLockingEngineClient(conn.ClientConn()),
		endpoint: settings.Endpoint,
		log:      settings.Logger,
	}, nil
}

func (c *Client) Endpoint() string {
	return c.endpoint
}

func (c *Client) Timeout() time.Duration {
	return c.conn.Timeout()
}

func (c *Client) String() string {
	return "dls.Client[" + c.endpoint + "]"
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) WithTransaction(ctx context.Context, fn TransactionFunc, options *TransactionOptions) error {
	return runTransaction(ctx, c, fn, options)
}

func (c *Client) BeginTransaction(ctx context.Context, options *BeginTransactionOptions) (*Transaction, error) {
	request := &dlspb.BeginTransactionRequest{}
	if options != nil && options.Timeout != 0 {
		request.TimeoutMs = proto.Int64(millis(options.Timeout))
	}
	var response *dlspb.BeginTransactionResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.BeginTransaction(ctx, request, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return &Transaction{TransactionID: response.GetTransactionId()}, nil
}

func (c *Client) AcquireLock(ctx context.Context, namespace, lockKey, transactionID string, mode LockMode, options *AcquireLockOptions) (*LockHolder, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options == nil {
		options = &AcquireLockOptions{}
	}
	wireMode := toWireMode(mode)
	request := &dlspb.AcquireLockRequest{
		Namespace:     namespace,
		LockKey:       lockKey,
		RequestedMode: &wireMode,
		TransactionId: transactionID,
	}
	if options.IdempotencyKey != "" {
		request.IdempotencyKey = proto.String(options.IdempotencyKey)
	}
	response, err := c.acquireStream(ctx, request, options)
	if err != nil {
		return nil, err
	}
	return assertAcquired(&LockHolder{
		LockID:       response.GetLockId(),
		FencingToken: decodeFencingToken(response.GetFencingToken()),
		Status:       toLockStatus(response.GetStatus()),
	}, namespace, lockKey)
}

func (c *Client) RenewTransaction(ctx context.Context, transactionID string, extra time.Duration) (*Transaction, error) {
	request := &dlspb.RenewTransactionRequest{TransactionId: transactionID, ExtraMs: millis(extra)}
	var response *dlspb.RenewTransactionResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.RenewTransaction(ctx, request, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return &Transaction{TransactionID: response.GetTransactionId()}, nil
}

func (c *Client) ReleaseLock(ctx context.Context, lockID, transactionID string) error {
	return c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) error {
		_, err := c.engine.ReleaseLock(ctx, &dlspb.ReleaseLockRequest{LockId: lockID, TransactionId: transactionID}, opts...)
		return err
	})
}

func (c *Client) ReleaseTransactionLocks(ctx context.Context, transactionID string) error {
	return c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) error {
		_, err := c.engine.ReleaseTransactionLocks(ctx, &dlspb.ReleaseTransactionLocksRequest{TransactionId: transactionID}, opts...)
		return err
	})
}

func (c *Client) GetLockStatus(ctx context.Context, namespace, lockKey string) (*LockStatusResponse, error) {
	var response *dlspb.GetLockStatusResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.GetLockStatus(ctx, &dlspb.GetLockStatusRequest{Namespace: namespace, LockKey: lockKey}, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return toLockStatusResponse(response), nil
}

func (c *Client) GetTransactionStatus(ctx context.Context, transactionID string) (*TransactionStatusResponse, error) {
	var response *dlspb.GetTransactionStatusResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.GetTransactionStatus(ctx, &dlspb.GetTransactionStatusRequest{TransactionId: transactionID}, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return toTransactionStatus(response), nil
}

func (c *Client) logger() *slog.Logger {
	return c.log
}

func (c *Client) acquireStream(ctx context.Context, request *dlspb.AcquireLockRequest, options *AcquireLockOptions) (*dlspb.AcquireLockResponse, error) {
	if c.conn.Closed() {
		return nil, closedError("dls.Client")
	}
	if ctx.Err() != nil {
		return nil, abortedError("", context.Cause(ctx))
	}
	requestID := transport.NewRequestID()
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = c.conn.Timeout()
	}
	streamCtx, cancel := context.WithTimeout(c.conn.Outgoing(ctx, requestID), timeout)
	defer cancel()
	stream, err := c.engine.AcquireLock(streamCtx, request)
	if err != nil {
		return nil, streamFailure(ctx, err, nil, requestID)
	}
	notified := false
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			failure := transport.Describe(nil, stream.Trailer(), requestID, "Stream ended without terminal status")
			return nil, NewError(failure.Message, caerus.CodeUnknown, &caerus.ErrorOptions{RequestID: failure.RequestID})
		}
		if err != nil {
			return nil, streamFailure(ctx, err, stream.Trailer(), requestID)
		}
		switch response.GetStatus() {
		case dlspb.LockStatus_ACQUIRED, dlspb.LockStatus_DENIED:
			return response, nil
		case dlspb.LockStatus_QUEUED:
			if !notified {
				notified = true
				notifyQueued(c.log, options.OnQueued)
			}
		default:
			c.log.Error("Received unspecified lock status in stream")
		}
	}
}

func streamFailure(ctx context.Context, err error, trailer metadata.MD, requestID string) error {
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return abortedError(requestID, context.Cause(ctx))
	}
	return fromFailure(transport.Describe(err, trailer, requestID, fallbackMessage))
}

func notifyQueued(log *slog.Logger, onQueued func()) {
	if onQueued == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Error("onQueued callback threw", "panic", recovered)
		}
	}()
	onQueued()
}

func (c *Client) call(ctx context.Context, invoke func(ctx context.Context, opts ...grpc.CallOption) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.conn.Closed() {
		return closedError("dls.Client")
	}
	requestID := transport.NewRequestID()
	callCtx, cancel := context.WithTimeout(c.conn.Outgoing(ctx, requestID), c.conn.Timeout())
	defer cancel()
	var trailer metadata.MD
	if err := invoke(callCtx, grpc.Trailer(&trailer)); err != nil {
		return fromFailure(transport.Describe(err, trailer, requestID, fallbackMessage))
	}
	return nil
}
