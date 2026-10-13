package caerus

import "strings"

type Code string

const (
	CodeResourceNotFound Code = "RESOURCE_NOT_FOUND"
	CodeConflict         Code = "CONFLICT"
	CodeValidation       Code = "VALIDATION"
	CodeAuthentication   Code = "AUTHENTICATION"
	CodeTimeout          Code = "TIMEOUT"
	CodeUnknown          Code = "UNKNOWN"
)

const (
	ReasonOutOfStock                 = "OUT_OF_STOCK"
	ReasonHolderNotActive            = "HOLDER_NOT_ACTIVE"
	ReasonResourceHasActiveHolds     = "RESOURCE_HAS_ACTIVE_HOLDS"
	ReasonResourceHasQueuedRequests  = "RESOURCE_HAS_QUEUED_REQUESTS"
	ReasonTemplateNotFound           = "TEMPLATE_NOT_FOUND"
	ReasonDeadlockDetected           = "DEADLOCK_DETECTED"
	ReasonTransactionNotActive       = "TRANSACTION_NOT_ACTIVE"
	ReasonLockAlreadyHeldExclusively = "LOCK_ALREADY_HELD_EXCLUSIVELY"
	ReasonLockModeMismatch           = "LOCK_MODE_MISMATCH"
	ReasonLockAcquisitionCancelled   = "LOCK_ACQUISITION_CANCELLED"
	ReasonLockDenied                 = "LOCK_DENIED"
	ReasonIdempotencyKeyRequired     = "IDEMPOTENCY_KEY_REQUIRED"
	ReasonResourceNotFound           = "RESOURCE_NOT_FOUND"
)

const docURL = "https://github.com/caerus-dev/caerus-sdk-go/blob/main/docs/errores.md"

type ErrorOptions struct {
	Reason    string
	RequestID string
	Cause     error
}

type Error struct {
	message   string
	code      Code
	reason    string
	requestID string
	cause     error
}

func NewError(message string, code Code, options *ErrorOptions) *Error {
	if code == "" {
		code = CodeUnknown
	}
	var resolved ErrorOptions
	if options != nil {
		resolved = *options
	}
	return &Error{
		message:   enrich(message, resolved.RequestID),
		code:      code,
		reason:    resolved.Reason,
		requestID: resolved.RequestID,
		cause:     resolved.Cause,
	}
}

func (e *Error) Error() string {
	return e.message
}

func (e *Error) Code() Code {
	return e.code
}

func (e *Error) Reason() string {
	return e.reason
}

func (e *Error) RequestID() string {
	return e.requestID
}

func (e *Error) DocURL() string {
	return docURL
}

func (e *Error) Unwrap() error {
	return e.cause
}

func enrich(message, requestID string) string {
	if requestID == "" || strings.Contains(message, requestID) {
		return message
	}
	return message + " (Request ID: " + requestID + "). Contacta soporte en Discord con este ID."
}
