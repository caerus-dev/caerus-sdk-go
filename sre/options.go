package sre

import (
	"log/slog"
	"time"
)

type ClientOptions struct {
	APIKey   string
	Endpoint string
	TLS      *bool
	Timeout  time.Duration
	Logger   *slog.Logger
}

func (o ClientOptions) String() string {
	return "sre.ClientOptions{Endpoint: " + o.Endpoint + ", Timeout: " + o.Timeout.String() + "}"
}

func (o ClientOptions) GoString() string {
	return o.String()
}

type CreateResourceOptions struct {
	GroupKey string
	Metadata map[string]any
}

type UpdateResourceOptions struct {
	GroupKey       string
	Metadata       map[string]any
	IdempotencyKey string
}

type TakeOptions struct {
	IdempotencyKey string
	TTL            time.Duration
	Metadata       map[string]any
}

type ConfirmOptions struct {
	Metadata map[string]any
}

type GetResourcesByGroupOptions struct {
	Page     int
	PageSize int
}

type ListResourceHoldersOptions struct {
	ResourceKey string
	Status      ResourceHolderStatus
	Sort        HolderSort
	Page        int
	PageSize    int
}
