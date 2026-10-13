package caerus_test

import (
	"errors"
	"regexp"
	"testing"

	"github.com/caerus-dev/caerus-sdk-go"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

func TestEnrichesTheMessageWithTheRequestIDAndTheSupportCopy(t *testing.T) {
	err := caerus.NewError("Out of stock", caerus.CodeConflict, &caerus.ErrorOptions{RequestID: "req_abc12345"})
	te.Equal(t, err.Error(), "Out of stock (Request ID: req_abc12345). Contacta soporte en Discord con este ID.")
	te.Equal(t, err.RequestID(), "req_abc12345")
}

func TestDoesNotRepeatARequestIDTheMessageAlreadyHas(t *testing.T) {
	err := caerus.NewError("failed for req_abc12345", caerus.CodeUnknown, &caerus.ErrorOptions{RequestID: "req_abc12345"})
	te.Equal(t, err.Error(), "failed for req_abc12345")
}

func TestLeavesTheMessageAloneWithoutARequestID(t *testing.T) {
	te.Equal(t, caerus.NewError("plain", caerus.CodeUnknown, nil).Error(), "plain")
}

func TestPointsAtTheErrorGuide(t *testing.T) {
	te.Equal(t, caerus.NewError("x", caerus.CodeUnknown, nil).DocURL(), "https://github.com/caerus-dev/caerus-sdk-go/blob/main/docs/errores.md")
}

func TestKeepsTheCauseAndTheReason(t *testing.T) {
	cause := errors.New("socket closed")
	err := caerus.NewError("x", caerus.CodeTimeout, &caerus.ErrorOptions{Cause: cause, Reason: "WHY"})
	te.True(t, errors.Is(err, cause), "the cause should be reachable")
	te.Equal(t, err.Reason(), "WHY")
	te.Equal(t, err.Code(), caerus.CodeTimeout)
}

func TestAMissingCodeIsUnknown(t *testing.T) {
	te.Equal(t, caerus.NewError("x", "", nil).Code(), caerus.CodeUnknown)
}

func TestTheVersionIsSemantic(t *testing.T) {
	te.True(t, regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(caerus.Version), "version %q", caerus.Version)
}
