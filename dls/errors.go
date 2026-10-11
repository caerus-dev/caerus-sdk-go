package dls

import (
	"errors"

	"google.golang.org/grpc/codes"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/transport"
)

const abortedByUser = "AcquireLock aborted by user"

var ErrTransactionClosed = errors.New("transaction context already closed")

type base = caerus.Error

type Error struct{ *base }

type NotFoundError struct{ *base }

type ConflictError struct{ *base }

type ValidationError struct{ *base }

type AuthenticationError struct{ *base }

type TimeoutError struct{ *base }

type DeadlockAbortedError struct{ *base }

type LockDeniedError struct{ *base }

type LockAlreadyHeldError struct{ *base }

type TransactionNotActiveError struct{ *base }

type LockModeMismatchError struct{ *base }

type LockAcquisitionCancelledError struct{ *base }

func (e *Error) Unwrap() error                         { return e.base }
func (e *NotFoundError) Unwrap() error                 { return &Error{e.base} }
func (e *ConflictError) Unwrap() error                 { return &Error{e.base} }
func (e *ValidationError) Unwrap() error               { return &Error{e.base} }
func (e *AuthenticationError) Unwrap() error           { return &Error{e.base} }
func (e *TimeoutError) Unwrap() error                  { return &Error{e.base} }
func (e *DeadlockAbortedError) Unwrap() error          { return &ConflictError{e.base} }
func (e *LockDeniedError) Unwrap() error               { return &ConflictError{e.base} }
func (e *LockAlreadyHeldError) Unwrap() error          { return &ConflictError{e.base} }
func (e *TransactionNotActiveError) Unwrap() error     { return &ConflictError{e.base} }
func (e *LockModeMismatchError) Unwrap() error         { return &ValidationError{e.base} }
func (e *LockAcquisitionCancelledError) Unwrap() error { return &Error{e.base} }

func NewError(message string, code caerus.Code, options *caerus.ErrorOptions) *Error {
	return &Error{caerus.NewError(message, code, options)}
}

func NewNotFoundError(message string, options *caerus.ErrorOptions) *NotFoundError {
	return &NotFoundError{caerus.NewError(message, caerus.CodeResourceNotFound, options)}
}

func NewConflictError(message string, options *caerus.ErrorOptions) *ConflictError {
	return &ConflictError{caerus.NewError(message, caerus.CodeConflict, options)}
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

func NewDeadlockAbortedError(message string, options *caerus.ErrorOptions) *DeadlockAbortedError {
	return &DeadlockAbortedError{caerus.NewError(message, caerus.CodeConflict, options)}
}

func NewLockDeniedError(message string, options *caerus.ErrorOptions) *LockDeniedError {
	return &LockDeniedError{caerus.NewError(message, caerus.CodeConflict, options)}
}

func NewLockAlreadyHeldError(message string, options *caerus.ErrorOptions) *LockAlreadyHeldError {
	return &LockAlreadyHeldError{caerus.NewError(message, caerus.CodeConflict, options)}
}

func NewTransactionNotActiveError(message string, options *caerus.ErrorOptions) *TransactionNotActiveError {
	return &TransactionNotActiveError{caerus.NewError(message, caerus.CodeConflict, options)}
}

func NewLockModeMismatchError(message string, options *caerus.ErrorOptions) *LockModeMismatchError {
	return &LockModeMismatchError{caerus.NewError(message, caerus.CodeValidation, options)}
}

func NewLockAcquisitionCancelledError(message string, options *caerus.ErrorOptions) *LockAcquisitionCancelledError {
	return &LockAcquisitionCancelledError{caerus.NewError(message, caerus.CodeUnknown, options)}
}

func fromFailure(failure transport.Failure) error {
	options := &caerus.ErrorOptions{Reason: failure.Reason, RequestID: failure.RequestID, Cause: failure.Cause}
	switch failure.Reason {
	case caerus.ReasonDeadlockDetected:
		return NewDeadlockAbortedError(failure.Message, options)
	case caerus.ReasonLockDenied:
		return NewLockDeniedError(failure.Message, options)
	case caerus.ReasonLockAlreadyHeldExclusively:
		return NewLockAlreadyHeldError(failure.Message, options)
	case caerus.ReasonLockModeMismatch:
		return NewLockModeMismatchError(failure.Message, options)
	case caerus.ReasonTransactionNotActive:
		return NewTransactionNotActiveError(failure.Message, options)
	case caerus.ReasonLockAcquisitionCancelled:
		return NewLockAcquisitionCancelledError(failure.Message, options)
	}
	if !failure.IsStatus {
		return NewError(failure.Message, caerus.CodeUnknown, options)
	}
	switch failure.Code {
	case codes.NotFound:
		return NewNotFoundError(failure.Message, options)
	case codes.AlreadyExists:
		return NewLockAlreadyHeldError(failure.Message, options)
	case codes.Aborted, codes.FailedPrecondition:
		return NewConflictError(failure.Message, options)
	case codes.InvalidArgument:
		return NewValidationError(failure.Message, options)
	case codes.Unauthenticated:
		return NewAuthenticationError(failure.Message, options)
	case codes.DeadlineExceeded:
		return NewTimeoutError(failure.Message, options)
	case codes.Canceled:
		return NewLockAcquisitionCancelledError(failure.Message, options)
	default:
		return NewError(failure.Message, caerus.CodeUnknown, options)
	}
}

func closedError(name string) error {
	return NewError("This "+name+" has been closed", caerus.CodeUnknown, nil)
}

func abortedError(requestID string, cause error) error {
	return NewError(abortedByUser, caerus.CodeUnknown, &caerus.ErrorOptions{RequestID: requestID, Cause: cause})
}

func isGone(err error) bool {
	var notFound *NotFoundError
	var notActive *TransactionNotActiveError
	var deadlock *DeadlockAbortedError
	return errors.As(err, &notFound) || errors.As(err, &notActive) || errors.As(err, &deadlock)
}
