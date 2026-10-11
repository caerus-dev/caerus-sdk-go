package sre

import "time"

type ResourceHolderStatus string

const (
	StatusPending   ResourceHolderStatus = "PENDING"
	StatusConfirmed ResourceHolderStatus = "CONFIRMED"
	StatusReleased  ResourceHolderStatus = "RELEASED"
	StatusExpired   ResourceHolderStatus = "EXPIRED"
	StatusQueued    ResourceHolderStatus = "QUEUED"
)

type HolderSort string

const (
	NewestFirst HolderSort = "NEWEST_FIRST"
	OldestFirst HolderSort = "OLDEST_FIRST"
)

type Resource struct {
	ID              string
	Key             string
	TemplateID      string
	AvailableAmount int
	PendingCount    int
	GroupKey        string
	Metadata        map[string]any
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ResourceHolder struct {
	ID         string
	ResourceID string
	Status     ResourceHolderStatus
	Amount     int
	ExpiresAt  time.Time
	Metadata   map[string]any
	CreatedAt  time.Time
}

type ResourcePage struct {
	Resources   []Resource
	HasNextPage bool
}

type ResourceHolderPage struct {
	Holders     []ResourceHolder
	HasNextPage bool
}
