package dls

import (
	"testing"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/dlspb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

func TestModesGoAndComeBackWithoutLosingAnything(t *testing.T) {
	for _, mode := range []LockMode{Exclusive, SharedRead} {
		te.Equal(t, toLockMode(toWireMode(mode)), mode)
	}
}

func TestAModeThatDoesNotExistDoesNotPassForAValidOne(t *testing.T) {
	te.Equal(t, toWireMode("WRITE"), dlspb.LockMode_MODE_UNSPECIFIED)
	te.Equal(t, toWireMode(""), dlspb.LockMode_MODE_UNSPECIFIED)
	te.Equal(t, toLockMode(dlspb.LockMode(7)), LockMode(""))
}

func TestTranslatesTheThreeStatesAndCallsTheRestUnknown(t *testing.T) {
	te.Equal(t, toLockStatus(dlspb.LockStatus_ACQUIRED), Acquired)
	te.Equal(t, toLockStatus(dlspb.LockStatus_DENIED), Denied)
	te.Equal(t, toLockStatus(dlspb.LockStatus_QUEUED), Queued)
	te.Equal(t, toLockStatus(dlspb.LockStatus_LOCK_STATUS_UNSPECIFIED), UnknownStatus)
	te.Equal(t, toLockStatus(dlspb.LockStatus(9)), UnknownStatus)
}

func TestAbsenceIsZeroAndARealFencingTokenPassesAsIs(t *testing.T) {
	te.Equal(t, decodeFencingToken(0), int64(0))
	te.Equal(t, decodeFencingToken(-1), int64(0))
	te.Equal(t, decodeFencingToken(35168), int64(35168))
}

func TestAssertAcquiredLetsAnAcquiredLockThrough(t *testing.T) {
	holder := &LockHolder{LockID: "l", FencingToken: 1, Status: Acquired}
	te.True(t, te.Must(assertAcquired(holder, "ns", "k")) == holder, "should be the same holder")
}

func TestAnUnknownStatusSaysItDoesNotKnowNotThatSomeoneElseHasIt(t *testing.T) {
	_, err := assertAcquired(&LockHolder{Status: UnknownStatus}, "ns", "k")
	dlsErr := te.As[*Error](t, err)
	te.Contains(t, dlsErr.Error(), "whether the lock was granted is unknown")
	te.Equal(t, dlsErr.Code(), caerus.CodeUnknown)
	te.NotAs[*LockDeniedError](t, err)
}

func TestAValidStatusThisCallCannotUseAlsoStops(t *testing.T) {
	_, err := assertAcquired(&LockHolder{Status: Queued}, "ns", "k")
	te.Contains(t, te.As[*ConflictError](t, err).Error(), "as QUEUED, which this call cannot use")
}
