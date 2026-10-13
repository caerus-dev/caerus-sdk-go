package sre

import (
	"google.golang.org/grpc/codes"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/transport"
)

type base = caerus.Error

type ResourceNotFoundError struct{ *base }

type ConflictError struct{ *base }

type OutOfStockError struct{ *base }

type HolderNotActiveError struct{ *base }

type ResourceHasActiveHoldsError struct{ *base }

type ResourceHasQueuedRequestsError struct{ *base }

type ValidationError struct{ *base }

type AuthenticationError struct{ *base }

type TimeoutError struct{ *base }

func (e *ResourceNotFoundError) Unwrap() error          { return e.base }
func (e *ConflictError) Unwrap() error                  { return e.base }
func (e *OutOfStockError) Unwrap() error                { return &ConflictError{e.base} }
func (e *HolderNotActiveError) Unwrap() error           { return &ConflictError{e.base} }
func (e *ResourceHasActiveHoldsError) Unwrap() error    { return &ConflictError{e.base} }
func (e *ResourceHasQueuedRequestsError) Unwrap() error { return &ConflictError{e.base} }
func (e *ValidationError) Unwrap() error                { return e.base }
func (e *AuthenticationError) Unwrap() error            { return e.base }
func (e *TimeoutError) Unwrap() error                   { return e.base }

func NewResourceNotFoundError(message string, options *caerus.ErrorOptions) *ResourceNotFoundError {
	return &ResourceNotFoundError{caerus.NewError(message, caerus.CodeResourceNotFound, options)}
}

func NewConflictError(message string, options *caerus.ErrorOptions) *ConflictError {
	return &ConflictError{caerus.NewError(message, caerus.CodeConflict, options)}
}

func NewOutOfStockError(message string, options *caerus.ErrorOptions) *OutOfStockError {
	return &OutOfStockError{caerus.NewError(message, caerus.CodeConflict, options)}
}

func NewHolderNotActiveError(message string, options *caerus.ErrorOptions) *HolderNotActiveError {
	return &HolderNotActiveError{caerus.NewError(message, caerus.CodeConflict, options)}
}

func NewResourceHasActiveHoldsError(message string, options *caerus.ErrorOptions) *ResourceHasActiveHoldsError {
	return &ResourceHasActiveHoldsError{caerus.NewError(message, caerus.CodeConflict, options)}
}

func NewResourceHasQueuedRequestsError(message string, options *caerus.ErrorOptions) *ResourceHasQueuedRequestsError {
	return &ResourceHasQueuedRequestsError{caerus.NewError(message, caerus.CodeConflict, options)}
}

func NewValidationError(message string, options *caerus.ErrorOptions) *ValidationError {
	return &ValidationError{caerus.NewError(message, caerus.CodeValidation, options)}
}

func NewAuthenticationError(message string, options *caerus.ErrorOptions) *AuthenticationError {
	return &AuthenticationError{caerus.NewError(message, caerus.CodeAuthentication, options)}
}

func NewTimeoutError(message string, options *caerus.ErrorOptions) *TimeoutError {
	return &TimeoutError{caerus.NewError(message, caerus.CodeTimeout, options)}
}

func fromFailure(failure transport.Failure) error {
	options := &caerus.ErrorOptions{Reason: failure.Reason, RequestID: failure.RequestID, Cause: failure.Cause}
	if !failure.IsStatus {
		return caerus.NewError(failure.Message, caerus.CodeUnknown, options)
	}
	switch failure.Code {
	case codes.NotFound:
		return NewResourceNotFoundError(failure.Message, options)
	case codes.FailedPrecondition:
		return conflict(failure.Reason, failure.Message, options)
	case codes.InvalidArgument:
		return NewValidationError(failure.Message, options)
	case codes.Unauthenticated:
		return NewAuthenticationError(failure.Message, options)
	case codes.DeadlineExceeded:
		return NewTimeoutError(failure.Message, options)
	default:
		return caerus.NewError(failure.Message, caerus.CodeUnknown, options)
	}
}

func conflict(reason, message string, options *caerus.ErrorOptions) error {
	switch reason {
	case caerus.ReasonOutOfStock:
		return NewOutOfStockError(message, options)
	case caerus.ReasonHolderNotActive:
		return NewHolderNotActiveError(message, options)
	case caerus.ReasonResourceHasActiveHolds:
		return NewResourceHasActiveHoldsError(message, options)
	case caerus.ReasonResourceHasQueuedRequests:
		return NewResourceHasQueuedRequestsError(message, options)
	default:
		return NewConflictError(message, options)
	}
}

func closedError(name string) error {
	return caerus.NewError("This "+name+" has been closed", caerus.CodeUnknown, nil)
}
