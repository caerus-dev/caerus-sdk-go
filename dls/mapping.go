package dls

import (
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
)

func toWireMode(mode LockMode) dlspb.LockMode {
	switch mode {
	case Exclusive:
		return dlspb.LockMode_EXCLUSIVE
	case SharedRead:
		return dlspb.LockMode_SHARED_READ
	default:
		return dlspb.LockMode_MODE_UNSPECIFIED
	}
}

func toLockMode(wire dlspb.LockMode) LockMode {
	switch wire {
	case dlspb.LockMode_EXCLUSIVE:
		return Exclusive
	case dlspb.LockMode_SHARED_READ:
		return SharedRead
	default:
		return ""
	}
}

func toLockStatus(wire dlspb.LockStatus) LockStatus {
	switch wire {
	case dlspb.LockStatus_ACQUIRED:
		return Acquired
	case dlspb.LockStatus_DENIED:
		return Denied
	case dlspb.LockStatus_QUEUED:
		return Queued
	default:
		return UnknownStatus
	}
}

func decodeFencingToken(raw int64) int64 {
	if raw > 0 {
		return raw
	}
	return 0
}

func assertAcquired(holder *LockHolder, namespace, lockKey string) (*LockHolder, error) {
	switch holder.Status {
	case Acquired:
		return holder, nil
	case Denied:
		return nil, NewLockDeniedError(
			"Caerus denied the lock on "+namespace+"/"+lockKey+": it is already held by another transaction.",
			&caerus.ErrorOptions{Reason: caerus.ReasonLockDenied})
	case UnknownStatus:
		return nil, NewError(
			"Caerus returned a lock status for "+namespace+"/"+lockKey+" that this SDK does not recognise, so whether the lock was granted is unknown.",
			caerus.CodeUnknown, nil)
	default:
		return nil, NewConflictError("Caerus returned the lock on "+namespace+"/"+lockKey+" as "+string(holder.Status)+", which this call cannot use.", nil)
	}
}

func millis(value time.Duration) int64 {
	if value <= 0 {
		return int64(value / time.Millisecond)
	}
	return int64((value + time.Millisecond - 1) / time.Millisecond)
}

func toTransactionStatus(response *dlspb.GetTransactionStatusResponse) *TransactionStatusResponse {
	locks := make([]TransactionLockInfo, 0, len(response.GetLocks()))
	for _, lock := range response.GetLocks() {
		mode := toLockMode(lock.GetRequestedMode())
		if mode == "" {
			mode = Exclusive
		}
		locks = append(locks, TransactionLockInfo{
			Namespace:     lock.GetNamespace(),
			LockKey:       lock.GetLockKey(),
			RequestedMode: mode,
			Status:        toLockStatus(lock.GetStatus()),
		})
	}
	return &TransactionStatusResponse{
		Status:      response.GetStatus(),
		AbortReason: response.GetAbortReason(),
		Locks:       locks,
		ExpiresAt:   response.GetExpiresAt(),
	}
}

func toLockStatusResponse(response *dlspb.GetLockStatusResponse) *LockStatusResponse {
	holders := make([]ActiveLockHolder, 0, len(response.GetActiveHolders()))
	for _, holder := range response.GetActiveHolders() {
		holders = append(holders, ActiveLockHolder{
			LockID:       holder.GetLockId(),
			ExpiresAt:    holder.GetExpiresAt(),
			FencingToken: decodeFencingToken(holder.GetFencingToken()),
		})
	}
	return &LockStatusResponse{
		IsHeld:           response.GetIsHeld(),
		CurrentMode:      toLockMode(response.GetCurrentMode()),
		ActiveHolders:    holders,
		PendingQueueSize: int(response.GetPendingQueueSize()),
	}
}
