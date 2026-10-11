package webhooks

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/caerus-dev/caerus-sdk-go/internal/jsonx"
)

type EventMeta struct {
	EventID       string
	OccurredOn    string
	EnvironmentID string
	APIKeyID      string
	ActorID       string
}

type EventData interface {
	Meta() EventMeta
	eventData()
}

func (m EventMeta) Meta() EventMeta {
	return m
}

func (EventMeta) eventData() {}

type ResourceCreatedData struct {
	EventMeta
	ResourceKey     string
	TemplateID      string
	AvailableAmount int
	GroupKey        string
	Metadata        string
	CreatedAtMs     int64
}

type ResourceTakenData struct {
	EventMeta
	HolderID       string
	ResourceKey    string
	Amount         int
	ExpiresAt      int64
	CreatedAtMs    int64
	Metadata       string
	IdempotencyKey string
}

type ResourceConfirmedData struct {
	EventMeta
	HolderID      string
	ResourceKey   string
	Amount        int
	ExpiresAt     int64
	CreatedAtMs   int64
	MetadataPatch string
}

type ResourceReleasedData struct {
	EventMeta
	HolderID    string
	ResourceKey string
	Amount      int
	ExpiresAt   int64
	CreatedAtMs int64
}

type ResourceExtendedData struct {
	EventMeta
	HolderID  string
	ExpiresAt int64
}

type ResourceExpiredData struct {
	EventMeta
	HolderID    string
	ResourceKey string
	Amount      int
	ExpiresAt   int64
	CreatedAtMs int64
}

type ResourceUpdatedData struct {
	EventMeta
	ResourceKey string
	DeltaAmount *int
	GroupKey    string
	Metadata    string
	UpdatedAtMs int64
}

type ResourceDeletedData struct {
	EventMeta
	ResourceKey string
}

type ResourceQueuedData struct {
	EventMeta
	HolderID       string
	ResourceKey    string
	Amount         int
	ExpiresAt      int64
	CreatedAtMs    int64
	Metadata       string
	IdempotencyKey string
}

type ResourceTakeFailedData struct {
	EventMeta
	ResourceID  string
	ResourceKey string
	Amount      int
	Reason      string
	CreatedAtMs int64
}

type LockAcquiredData struct {
	EventMeta
	Namespace     string
	LockKey       string
	LockID        string
	TransactionID string
	FencingToken  int64
	Mode          string
}

type LockReleasedData struct {
	EventMeta
	Namespace     string
	LockKey       string
	LockID        string
	TransactionID string
}

type LockAcquireFailedData struct {
	EventMeta
	Namespace     string
	LockKey       string
	TransactionID string
	Reason        string
}

type DeadlockDetectedData struct {
	EventMeta
	VictimTransactionID string
	CycleTransactionIDs []string
	ResolutionStrategy  string
	Reason              string
}

type LockAbandonedData struct {
	EventMeta
	TransactionID string
	LockIDs       []string
	Reason        string
}

type TransactionStartedData struct {
	EventMeta
	TransactionID        string
	TransactionTimeoutMs int64
}

type TransactionCompletedData struct {
	EventMeta
	TransactionID string
}

type TransactionAbortedData struct {
	EventMeta
	TransactionID string
	Reason        string
}

type TransactionRenewedData struct {
	EventMeta
	TransactionID string
	NewLifetimeMs int64
}

type TransactionExpiredData struct {
	EventMeta
	TransactionID string
	Reason        string
}

type UnknownEventData struct {
	EventMeta
	Fields map[string]any
}

func decode(eventType string, f fields, raw map[string]any) EventData {
	meta := EventMeta{
		EventID:       f.str("eventId"),
		OccurredOn:    f.str("occurredOn"),
		EnvironmentID: f.str("environmentId"),
		APIKeyID:      f.str("apiKeyId"),
		ActorID:       f.str("actorId"),
	}
	switch eventType {
	case "resource.created":
		return &ResourceCreatedData{meta, f.str("resourceKey"), f.str("templateId"), f.integer("availableAmount"), f.str("groupKey"), f.str("metadata"), f.long("createdAtMs")}
	case "resource.taken":
		return &ResourceTakenData{meta, f.str("holderId"), f.str("resourceKey"), f.integer("amount"), f.long("expiresAt"), f.long("createdAtMs"), f.str("metadata"), f.str("idempotencyKey")}
	case "resource.confirmed":
		return &ResourceConfirmedData{meta, f.str("holderId"), f.str("resourceKey"), f.integer("amount"), f.long("expiresAt"), f.long("createdAtMs"), f.str("metadataPatch")}
	case "resource.released":
		return &ResourceReleasedData{meta, f.str("holderId"), f.str("resourceKey"), f.integer("amount"), f.long("expiresAt"), f.long("createdAtMs")}
	case "resource.extended":
		return &ResourceExtendedData{meta, f.str("holderId"), f.long("expiresAt")}
	case "resource.expired":
		return &ResourceExpiredData{meta, f.str("holderId"), f.str("resourceKey"), f.integer("amount"), f.long("expiresAt"), f.long("createdAtMs")}
	case "resource.updated":
		return &ResourceUpdatedData{meta, f.str("resourceKey"), f.optionalInteger("deltaAmount"), f.str("groupKey"), f.str("metadata"), f.long("updatedAtMs")}
	case "resource.deleted":
		return &ResourceDeletedData{meta, f.str("resourceKey")}
	case "resource.queued":
		return &ResourceQueuedData{meta, f.str("holderId"), f.str("resourceKey"), f.integer("amount"), f.long("expiresAt"), f.long("createdAtMs"), f.str("metadata"), f.str("idempotencyKey")}
	case "resource.take_failed":
		return &ResourceTakeFailedData{meta, f.str("resourceId"), f.str("resourceKey"), f.integer("amount"), f.str("reason"), f.long("createdAtMs")}
	case "lock.acquired":
		return &LockAcquiredData{meta, f.str("namespace"), f.str("lockKey"), f.str("lockId"), f.str("transactionId"), f.long("fencingToken"), f.str("mode")}
	case "lock.released":
		return &LockReleasedData{meta, f.str("namespace"), f.str("lockKey"), f.str("lockId"), f.str("transactionId")}
	case "lock.acquire_failed":
		return &LockAcquireFailedData{meta, f.str("namespace"), f.str("lockKey"), f.str("transactionId"), f.str("reason")}
	case "lock.deadlock_detected":
		return &DeadlockDetectedData{meta, f.str("victimTransactionId"), f.strings("cycleTransactionIds"), f.str("resolutionStrategy"), f.str("reason")}
	case "lock.abandoned":
		return &LockAbandonedData{meta, f.str("transactionId"), f.strings("lockIds"), f.str("reason")}
	case "transaction.started":
		return &TransactionStartedData{meta, f.str("transactionId"), f.long("transactionTimeoutMs")}
	case "transaction.completed":
		return &TransactionCompletedData{meta, f.str("transactionId")}
	case "transaction.aborted":
		return &TransactionAbortedData{meta, f.str("transactionId"), f.str("reason")}
	case "transaction.renewed":
		return &TransactionRenewedData{meta, f.str("transactionId"), f.long("newLifetimeMs")}
	case "transaction.expired":
		return &TransactionExpiredData{meta, f.str("transactionId"), f.str("reason")}
	default:
		if raw == nil {
			raw = map[string]any{}
		}
		return &UnknownEventData{meta, raw}
	}
}

type fields map[string]any

func (f fields) str(name string) string {
	value, ok := f[name]
	if !ok || value == nil {
		return ""
	}
	return text(value)
}

func text(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	default:
		encoded, err := jsonx.Stringify(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return encoded
	}
}

func (f fields) number(name string) (float64, int64, bool, bool) {
	switch typed := f[name].(type) {
	case json.Number:
		if whole, err := typed.Int64(); err == nil {
			return float64(whole), whole, true, true
		}
		if float, err := typed.Float64(); err == nil {
			return float, 0, false, true
		}
	case string:
		if float, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return float, 0, false, true
		}
	}
	return 0, 0, false, false
}

func (f fields) long(name string) int64 {
	float, whole, isWhole, ok := f.number(name)
	if !ok {
		return 0
	}
	if isWhole {
		return whole
	}
	return truncate(float, math.MinInt64, math.MaxInt64)
}

func (f fields) integer(name string) int {
	value := f.optionalInteger(name)
	if value == nil {
		return 0
	}
	return *value
}

func (f fields) optionalInteger(name string) *int {
	float, whole, isWhole, ok := f.number(name)
	if !ok {
		return nil
	}
	var value int
	if isWhole {
		value = int(int32(whole))
	} else {
		value = int(truncate(float, math.MinInt32, math.MaxInt32))
	}
	return &value
}

func (f fields) strings(name string) []string {
	items, ok := f[name].([]any)
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil {
			result = append(result, text(item))
		}
	}
	return result
}

func truncate(value float64, low, high int64) int64 {
	switch {
	case math.IsNaN(value):
		return 0
	case value <= float64(low):
		return low
	case value >= float64(high):
		return high
	default:
		return int64(value)
	}
}
