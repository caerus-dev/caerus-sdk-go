package webhooks

import "github.com/caerus-dev/caerus-sdk-go"

type base = caerus.Error

type SignatureError struct{ *base }

type ExpiredError struct{ *base }

type PayloadError struct{ *base }

func (e *SignatureError) Unwrap() error { return e.base }
func (e *ExpiredError) Unwrap() error   { return e.base }
func (e *PayloadError) Unwrap() error   { return e.base }

func NewSignatureError(message string, options *caerus.ErrorOptions) *SignatureError {
	return &SignatureError{caerus.NewError(message, caerus.CodeValidation, options)}
}

func NewExpiredError(message string, options *caerus.ErrorOptions) *ExpiredError {
	return &ExpiredError{caerus.NewError(message, caerus.CodeValidation, options)}
}

func NewPayloadError(message string, options *caerus.ErrorOptions) *PayloadError {
	return &PayloadError{caerus.NewError(message, caerus.CodeValidation, options)}
}
