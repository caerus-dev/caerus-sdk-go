package sre

import (
	"fmt"
	"strings"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/jsonx"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb"
)

func encodeMetadata(metadata map[string]any) (*string, error) {
	if metadata == nil {
		return nil, nil
	}
	text, err := jsonx.Stringify(metadata)
	if err != nil {
		return nil, NewValidationError("metadata cannot be serialized as JSON: "+err.Error(), &caerus.ErrorOptions{Cause: err})
	}
	return &text, nil
}

func decodeMetadata(raw *string, context string) (map[string]any, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	object, err := jsonx.ParseObject(*raw)
	if err != nil {
		return nil, caerus.NewError(
			"The metadata stored for "+context+" is not a JSON object, so it cannot be returned as one. Raw value: "+*raw,
			caerus.CodeUnknown,
			&caerus.ErrorOptions{Cause: err})
	}
	return object, nil
}

func decodeTimestampMs(epochMs int64) time.Time {
	if epochMs == 0 {
		return time.Time{}
	}
	return time.UnixMilli(epochMs)
}

func decodeStatus(wire srepb.ResourceHolderResponse_ResourceHolderStatus, context string) (ResourceHolderStatus, error) {
	switch wire {
	case srepb.ResourceHolderResponse_PENDING:
		return StatusPending, nil
	case srepb.ResourceHolderResponse_CONFIRMED:
		return StatusConfirmed, nil
	case srepb.ResourceHolderResponse_RELEASED:
		return StatusReleased, nil
	case srepb.ResourceHolderResponse_QUEUED:
		return StatusQueued, nil
	case srepb.ResourceHolderResponse_EXPIRED:
		return StatusExpired, nil
	default:
		return "", caerus.NewError(
			fmt.Sprintf("Caerus reported an unknown status (%d) for %s. Update github.com/caerus-dev/caerus-sdk-go.", int32(wire), context),
			caerus.CodeUnknown, nil)
	}
}

func encodeStatus(status ResourceHolderStatus) (srepb.ResourceHolderResponse_ResourceHolderStatus, error) {
	switch status {
	case StatusPending:
		return srepb.ResourceHolderResponse_PENDING, nil
	case StatusConfirmed:
		return srepb.ResourceHolderResponse_CONFIRMED, nil
	case StatusReleased:
		return srepb.ResourceHolderResponse_RELEASED, nil
	case StatusQueued:
		return srepb.ResourceHolderResponse_QUEUED, nil
	case StatusExpired:
		return srepb.ResourceHolderResponse_EXPIRED, nil
	default:
		return 0, NewValidationError("status "+string(status)+" is not a ResourceHolderStatus", nil)
	}
}

func toResourceHolder(response *srepb.ResourceHolderResponse) (*ResourceHolder, error) {
	context := "holder " + response.GetHolderId()
	status, err := decodeStatus(response.GetStatus(), context)
	if err != nil {
		return nil, err
	}
	metadata, err := decodeMetadata(response.Metadata, context)
	if err != nil {
		return nil, err
	}
	return &ResourceHolder{
		ID:         response.GetHolderId(),
		ResourceID: response.GetResourceId(),
		Status:     status,
		Amount:     int(response.GetAmount()),
		ExpiresAt:  time.Unix(response.GetExpiresAt(), 0),
		Metadata:   metadata,
		CreatedAt:  decodeTimestampMs(response.GetCreatedAtMs()),
	}, nil
}

func toResource(response *srepb.ResourceResponse) (*Resource, error) {
	metadata, err := decodeMetadata(response.Metadata, "resource "+response.GetKey())
	if err != nil {
		return nil, err
	}
	return &Resource{
		ID:              response.GetResourceId(),
		Key:             response.GetKey(),
		TemplateID:      response.GetTemplateId(),
		AvailableAmount: int(response.GetAvailableAmount()),
		PendingCount:    int(response.GetPendingCount()),
		GroupKey:        response.GetGroupKey(),
		Metadata:        metadata,
		CreatedAt:       decodeTimestampMs(response.GetCreatedAtMs()),
		UpdatedAt:       decodeTimestampMs(response.GetUpdatedAtMs()),
	}, nil
}

func toResourcePage(response *srepb.GetResourcesByGroupKeyResponse) (*ResourcePage, error) {
	resources := make([]Resource, 0, len(response.GetResources()))
	for _, item := range response.GetResources() {
		resource, err := toResource(item)
		if err != nil {
			return nil, err
		}
		resources = append(resources, *resource)
	}
	return &ResourcePage{Resources: resources, HasNextPage: response.GetNextPage()}, nil
}

func toResourceHolderPage(response *srepb.GetResourceHoldersListResponse) (*ResourceHolderPage, error) {
	holders := make([]ResourceHolder, 0, len(response.GetResourceHolders()))
	for _, item := range response.GetResourceHolders() {
		holder, err := toResourceHolder(item)
		if err != nil {
			return nil, err
		}
		holders = append(holders, *holder)
	}
	return &ResourceHolderPage{Holders: holders, HasNextPage: response.GetNextPage()}, nil
}

func assertUsable(holder *ResourceHolder, usable ...ResourceHolderStatus) (*ResourceHolder, error) {
	for _, status := range usable {
		if holder.Status == status {
			return holder, nil
		}
	}
	return nil, NewConflictError("Caerus returned holder "+holder.ID+" as "+string(holder.Status)+", which this call cannot use.", nil)
}

func requireText(value, name string) error {
	if strings.TrimSpace(value) == "" {
		return NewValidationError(name+" is required", nil)
	}
	return nil
}

func requirePositive(value int64, name string) error {
	if value <= 0 {
		return NewValidationError(name+" must be greater than zero", nil)
	}
	return nil
}

func ceilDiv(value, unit time.Duration) int64 {
	if value <= 0 {
		return int64(value / unit)
	}
	return int64((value + unit - 1) / unit)
}
