package sre

import (
	"context"
	"math"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/caerus-dev/caerus-sdk-go/internal/config"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb"
	"github.com/caerus-dev/caerus-sdk-go/internal/transport"
)

type Client struct {
	conn     *transport.Conn
	engine   srepb.SharedResourceEngineClient
	endpoint string
}

func NewClient(options ClientOptions) (*Client, error) {
	return newClient(options, config.System)
}

func newClient(options ClientOptions, env config.Environment, dial ...grpc.DialOption) (*Client, error) {
	settings, err := config.Resolve(config.SRE, config.Input{
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
	return &Client{conn: conn, engine: srepb.NewSharedResourceEngineClient(conn.ClientConn()), endpoint: settings.Endpoint}, nil
}

func (c *Client) Endpoint() string {
	return c.endpoint
}

func (c *Client) Timeout() time.Duration {
	return c.conn.Timeout()
}

func (c *Client) String() string {
	return "sre.Client[" + c.endpoint + "]"
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) CreateUnitary(ctx context.Context, templateName, key string, options *CreateResourceOptions) (*Resource, error) {
	return c.createResource(ctx, templateName, key, 1, options)
}

func (c *Client) CreateMultiple(ctx context.Context, templateName, key string, availableAmount int, options *CreateResourceOptions) (*Resource, error) {
	if err := requirePositive(int64(availableAmount), "availableAmount"); err != nil {
		return nil, err
	}
	return c.createResource(ctx, templateName, key, availableAmount, options)
}

func (c *Client) createResource(ctx context.Context, templateName, key string, availableAmount int, options *CreateResourceOptions) (*Resource, error) {
	if err := requireText(templateName, "templateName"); err != nil {
		return nil, err
	}
	if err := requireText(key, "key"); err != nil {
		return nil, err
	}
	amount, err := toInt32(availableAmount, "availableAmount")
	if err != nil {
		return nil, err
	}
	if options == nil {
		options = &CreateResourceOptions{}
	}
	request := &srepb.CreateResourceRequest{TemplateName: templateName, Key: key, AvailableAmount: amount}
	if options.GroupKey != "" {
		request.GroupKey = proto.String(options.GroupKey)
	}
	if request.Metadata, err = encodeMetadata(options.Metadata); err != nil {
		return nil, err
	}
	var response *srepb.ResourceResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.CreateResource(ctx, request, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return toResource(response)
}

func (c *Client) UpdateResource(ctx context.Context, key string, deltaAmount int, options *UpdateResourceOptions) (*Resource, error) {
	if err := requireText(key, "key"); err != nil {
		return nil, err
	}
	delta, err := toInt32(deltaAmount, "deltaAmount")
	if err != nil {
		return nil, err
	}
	if options == nil {
		options = &UpdateResourceOptions{}
	}
	request := &srepb.UpdateResourceRequest{ResourceKey: key, DeltaAmount: delta}
	if options.GroupKey != "" {
		request.GroupKey = proto.String(options.GroupKey)
	}
	if options.IdempotencyKey != "" {
		request.IdempotencyKey = proto.String(options.IdempotencyKey)
	}
	if request.Metadata, err = encodeMetadata(options.Metadata); err != nil {
		return nil, err
	}
	var response *srepb.ResourceResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.UpdateResource(ctx, request, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return toResource(response)
}

func (c *Client) DeleteResource(ctx context.Context, key string) error {
	if err := requireText(key, "key"); err != nil {
		return err
	}
	return c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) error {
		_, err := c.engine.DeleteResource(ctx, &srepb.DeleteResourceRequest{Key: key}, opts...)
		return err
	})
}

func (c *Client) Unitary(key string) *UnitaryResource {
	return &UnitaryResource{key: key, take: c.take}
}

func (c *Client) Pooled(key string) *PooledResource {
	return &PooledResource{key: key, take: c.take}
}

func (c *Client) take(ctx context.Context, key string, amount int, options *TakeOptions) (*ResourceHolder, error) {
	if options == nil {
		options = &TakeOptions{}
	}
	count, err := toInt32(amount, "amount")
	if err != nil {
		return nil, err
	}
	settings := &srepb.TakeOptionalSettings{}
	if options.TTL != 0 {
		seconds := ceilDiv(options.TTL, time.Second)
		if err := requirePositive(seconds, "TTL"); err != nil {
			return nil, err
		}
		ttl, err := toInt32(int(seconds), "TTL")
		if err != nil {
			return nil, err
		}
		settings.CustomTtlSeconds = proto.Int32(ttl)
	}
	if options.IdempotencyKey != "" {
		settings.IdempotencyKey = proto.String(options.IdempotencyKey)
	}
	if settings.Metadata, err = encodeMetadata(options.Metadata); err != nil {
		return nil, err
	}
	request := &srepb.TakeRequest{ResourceKey: key, Amount: count, Settings: settings}
	var response *srepb.ResourceHolderResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.Take(ctx, request, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	holder, err := toResourceHolder(response)
	if err != nil {
		return nil, err
	}
	return assertUsable(holder, StatusPending, StatusQueued)
}

func (c *Client) Confirm(ctx context.Context, resourceHolderID string, options *ConfirmOptions) (*ResourceHolder, error) {
	if err := requireText(resourceHolderID, "resourceHolderID"); err != nil {
		return nil, err
	}
	if options == nil {
		options = &ConfirmOptions{}
	}
	request := &srepb.ConfirmRequest{ResourceHolderId: resourceHolderID}
	var err error
	if request.MetadataPatch, err = encodeMetadata(options.Metadata); err != nil {
		return nil, err
	}
	var response *srepb.ResourceHolderResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.Confirm(ctx, request, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	holder, err := toResourceHolder(response)
	if err != nil {
		return nil, err
	}
	return assertUsable(holder, StatusConfirmed)
}

func (c *Client) Release(ctx context.Context, resourceHolderID string) error {
	if err := requireText(resourceHolderID, "resourceHolderID"); err != nil {
		return err
	}
	return c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) error {
		_, err := c.engine.Release(ctx, &srepb.ReleaseRequest{ResourceHolderId: resourceHolderID}, opts...)
		return err
	})
}

func (c *Client) Extend(ctx context.Context, resourceHolderID string, extra time.Duration) (*ResourceHolder, error) {
	if err := requireText(resourceHolderID, "resourceHolderID"); err != nil {
		return nil, err
	}
	millis := ceilDiv(extra, time.Millisecond)
	if err := requirePositive(millis, "extra"); err != nil {
		return nil, err
	}
	extraMs, err := toInt32(int(millis), "extra")
	if err != nil {
		return nil, err
	}
	request := &srepb.ExtendRequest{ResourceHolderId: resourceHolderID, ExtraMs: extraMs}
	var response *srepb.ResourceHolderResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.Extend(ctx, request, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	holder, err := toResourceHolder(response)
	if err != nil {
		return nil, err
	}
	return assertUsable(holder, StatusPending, StatusQueued)
}

func (c *Client) GetResourceHolder(ctx context.Context, resourceHolderID string) (*ResourceHolder, error) {
	if err := requireText(resourceHolderID, "resourceHolderID"); err != nil {
		return nil, err
	}
	var response *srepb.ResourceHolderResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.GetResourceHolder(ctx, &srepb.GetResourceHolderRequest{ResourceHolderId: resourceHolderID}, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return toResourceHolder(response)
}

func (c *Client) GetResource(ctx context.Context, key string) (*Resource, error) {
	if err := requireText(key, "key"); err != nil {
		return nil, err
	}
	var response *srepb.ResourceResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.GetResource(ctx, &srepb.GetResourceRequest{Key: key}, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return toResource(response)
}

func (c *Client) GetResourcesByGroup(ctx context.Context, groupKey string, options *GetResourcesByGroupOptions) (*ResourcePage, error) {
	if err := requireText(groupKey, "groupKey"); err != nil {
		return nil, err
	}
	if options == nil {
		options = &GetResourcesByGroupOptions{}
	}
	page, err := toInt32(options.Page, "Page")
	if err != nil {
		return nil, err
	}
	request := &srepb.GetResourcesByGroupKeyRequest{GroupKey: groupKey, Page: page}
	if options.PageSize != 0 {
		size, err := toInt32(options.PageSize, "PageSize")
		if err != nil {
			return nil, err
		}
		request.PageSize = proto.Int32(size)
	}
	var response *srepb.GetResourcesByGroupKeyResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.GetResourcesByGroupKey(ctx, request, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return toResourcePage(response)
}

func (c *Client) ListResourceHolders(ctx context.Context, options *ListResourceHoldersOptions) (*ResourceHolderPage, error) {
	if options == nil {
		options = &ListResourceHoldersOptions{}
	}
	page, err := toInt32(options.Page, "Page")
	if err != nil {
		return nil, err
	}
	direction := srepb.GetResourceHoldersListRequest_DESCENDING
	if options.Sort == OldestFirst {
		direction = srepb.GetResourceHoldersListRequest_ASCENDING
	}
	request := &srepb.GetResourceHoldersListRequest{Page: page, CreatedAtSortDirection: &direction}
	if options.ResourceKey != "" {
		request.ResourceKey = proto.String(options.ResourceKey)
	}
	if options.PageSize != 0 {
		size, err := toInt32(options.PageSize, "PageSize")
		if err != nil {
			return nil, err
		}
		request.PageSize = proto.Int32(size)
	}
	if options.Status != "" {
		status, err := encodeStatus(options.Status)
		if err != nil {
			return nil, err
		}
		request.StatusFilter = &status
	}
	var response *srepb.GetResourceHoldersListResponse
	if err := c.call(ctx, func(ctx context.Context, opts ...grpc.CallOption) (err error) {
		response, err = c.engine.GetResourceHoldersList(ctx, request, opts...)
		return err
	}); err != nil {
		return nil, err
	}
	return toResourceHolderPage(response)
}

func (c *Client) call(ctx context.Context, invoke func(ctx context.Context, opts ...grpc.CallOption) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.conn.Closed() {
		return closedError("sre.Client")
	}
	requestID := transport.NewRequestID()
	callCtx, cancel := context.WithTimeout(c.conn.Outgoing(ctx, requestID), c.conn.Timeout())
	defer cancel()
	var trailer metadata.MD
	if err := invoke(callCtx, grpc.Trailer(&trailer)); err != nil {
		return fromFailure(transport.Describe(err, trailer, requestID, "Caerus call failed"))
	}
	return nil
}

func toInt32(value int, name string) (int32, error) {
	if value > math.MaxInt32 || value < math.MinInt32 {
		return 0, NewValidationError(name+" is out of range", nil)
	}
	return int32(value), nil
}
