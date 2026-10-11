package sre

import (
	"context"
	"time"
)

type API interface {
	CreateUnitary(ctx context.Context, templateName, key string, options *CreateResourceOptions) (*Resource, error)
	CreateMultiple(ctx context.Context, templateName, key string, availableAmount int, options *CreateResourceOptions) (*Resource, error)
	UpdateResource(ctx context.Context, key string, deltaAmount int, options *UpdateResourceOptions) (*Resource, error)
	DeleteResource(ctx context.Context, key string) error
	Unitary(key string) *UnitaryResource
	Pooled(key string) *PooledResource
	Confirm(ctx context.Context, resourceHolderID string, options *ConfirmOptions) (*ResourceHolder, error)
	Release(ctx context.Context, resourceHolderID string) error
	Extend(ctx context.Context, resourceHolderID string, extra time.Duration) (*ResourceHolder, error)
	GetResourceHolder(ctx context.Context, resourceHolderID string) (*ResourceHolder, error)
	GetResource(ctx context.Context, key string) (*Resource, error)
	GetResourcesByGroup(ctx context.Context, groupKey string, options *GetResourcesByGroupOptions) (*ResourcePage, error)
	ListResourceHolders(ctx context.Context, options *ListResourceHoldersOptions) (*ResourceHolderPage, error)
	Close() error
}

type takeFunc func(ctx context.Context, key string, amount int, options *TakeOptions) (*ResourceHolder, error)

type UnitaryResource struct {
	key  string
	take takeFunc
}

type PooledResource struct {
	key  string
	take takeFunc
}

func (r *UnitaryResource) Key() string {
	return r.key
}

func (r *UnitaryResource) Take(ctx context.Context, options *TakeOptions) (*ResourceHolder, error) {
	if err := requireKey(r.key); err != nil {
		return nil, err
	}
	return r.take(ctx, r.key, 1, options)
}

func (r *UnitaryResource) String() string {
	return "UnitaryResource[" + r.key + "]"
}

func (r *PooledResource) Key() string {
	return r.key
}

func (r *PooledResource) Take(ctx context.Context, options *TakeOptions) (*ResourceHolder, error) {
	if err := requireKey(r.key); err != nil {
		return nil, err
	}
	return r.take(ctx, r.key, 1, options)
}

func (r *PooledResource) TakeMany(ctx context.Context, amount int, options *TakeOptions) (*ResourceHolder, error) {
	if err := requireKey(r.key); err != nil {
		return nil, err
	}
	if amount <= 0 {
		return nil, NewValidationError("amount must be greater than zero", nil)
	}
	return r.take(ctx, r.key, amount, options)
}

func (r *PooledResource) String() string {
	return "PooledResource[" + r.key + "]"
}

func requireKey(key string) error {
	if err := requireText(key, "key"); err != nil {
		return NewValidationError("a resource key is required", nil)
	}
	return nil
}

var (
	_ API = (*Client)(nil)
	_ API = (*InMemoryClient)(nil)
)
