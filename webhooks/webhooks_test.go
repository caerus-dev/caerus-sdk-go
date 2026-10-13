package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

const (
	secret   = "test_secret_key"
	nowEpoch = int64(1_790_000_000)
)

func TestMain(m *testing.M) {
	now = func() time.Time { return time.Unix(nowEpoch, 0) }
	m.Run()
}

func engineSign(timestamp int64, payload, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, payload)))
	return hex.EncodeToString(mac.Sum(nil))
}

func header(payload string, timestamp int64) string {
	return fmt.Sprintf("t=%d,v1=%s", timestamp, engineSign(timestamp, payload, secret))
}

func construct(t *testing.T, payload string) *Event {
	t.Helper()
	return te.Must(ConstructEvent([]byte(payload), header(payload, nowEpoch), secret, nil))
}

func TestConstructsAnEventWithAValidSignatureAndPayload(t *testing.T) {
	payload := `{"id":"evt_123","eventId":"evt_123","eventType":"resource.taken","product":"SRE",` +
		`"objectType":"RESOURCE_HOLDER","objectId":"h_123","environmentId":"env_abc",` +
		`"occurredAt":"2026-08-29T15:00:00Z","data":{"eventId":"evt_123","occurredOn":"2026-08-29T15:00:00Z",` +
		`"holderId":"h_123","resourceKey":"seat_A12","amount":1,"expiresAt":1000,` +
		`"createdAtMs":1700000000000,"idempotencyKey":null}}`
	event := construct(t, payload)
	te.Equal(t, event.EventType, "resource.taken")
	te.Equal(t, event.EventID, "evt_123")
	te.Equal(t, event.ID, "evt_123")
	te.Equal(t, event.Product, "SRE")
	te.Equal(t, event.ObjectType, "RESOURCE_HOLDER")
	te.Equal(t, event.ObjectID, "h_123")
	te.Equal(t, event.EnvironmentID, "env_abc")
	te.Equal(t, event.OccurredAt, "2026-08-29T15:00:00Z")
	data, ok := event.Data.(*ResourceTakenData)
	te.True(t, ok, "got %T", event.Data)
	te.Equal(t, *data, ResourceTakenData{
		EventMeta:   EventMeta{EventID: "evt_123", OccurredOn: "2026-08-29T15:00:00Z"},
		HolderID:    "h_123",
		ResourceKey: "seat_A12",
		Amount:      1,
		ExpiresAt:   1000,
		CreatedAtMs: 1_700_000_000_000,
	})
	te.Equal(t, event.Data.Meta().EventID, "evt_123")
}

func TestVerifiesTheRawUTF8Bytes(t *testing.T) {
	payload := `{"eventType":"resource.created","data":{"resourceKey":"butaca_ñ_€"}}`
	te.Equal(t, construct(t, payload).Data.(*ResourceCreatedData).ResourceKey, "butaca_ñ_€")
}

func TestVerifiesTheSignatureOverTheRawBytesEvenWhenTheyAreNotValidUTF8(t *testing.T) {
	raw := append(append([]byte(`{"eventType":"x","b":"`), 0xff), []byte(`"}`)...)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.", nowEpoch)))
	mac.Write(raw)
	signed := fmt.Sprintf("t=%d,v1=%s", nowEpoch, hex.EncodeToString(mac.Sum(nil)))
	_, err := ConstructEvent(raw, signed, secret, nil)
	te.Contains(t, te.As[*PayloadError](t, err).Error(), "UTF-8")
}

func TestFailsWithASignatureErrorWhenTheHeaderIsMissing(t *testing.T) {
	_, err := ConstructEvent([]byte("{}"), "", secret, nil)
	te.As[*SignatureError](t, err)
}

func TestFailsWithASignatureErrorOnAnInvalidFormat(t *testing.T) {
	for _, bad := range []string{"t=123,v2=invalid", "t=,v1=abc", "t=nope,v1=abc", "v1=abc"} {
		_, err := ConstructEvent([]byte("{}"), bad, secret, nil)
		te.Equal(t, te.As[*SignatureError](t, err).Error(), "Invalid signature format.")
	}
}

func TestFailsWithAnExpiredErrorOutsideTheToleranceZone(t *testing.T) {
	for _, timestamp := range []int64{nowEpoch - 360, nowEpoch + 360} {
		_, err := ConstructEvent([]byte("{}"), header("{}", timestamp), secret, &ConstructEventOptions{Tolerance: 300 * time.Second})
		te.As[*ExpiredError](t, err)
	}
}

func TestAcceptsAnEventWithinTheToleranceZoneAndTheDefaultIsFiveMinutes(t *testing.T) {
	payload := `{"eventType":"test"}`
	te.Equal(t, te.Must(ConstructEvent([]byte(payload), header(payload, nowEpoch-300), secret, nil)).EventType, "test")
	_, err := ConstructEvent([]byte(payload), header(payload, nowEpoch-301), secret, nil)
	te.As[*ExpiredError](t, err)
	te.Equal(t, DefaultTolerance, 5*time.Minute)
	te.Must(ConstructEvent([]byte(payload), header(payload, nowEpoch-1000), secret, &ConstructEventOptions{Tolerance: time.Hour}))
}

func TestSupportsKeyRolloverWithSeveralSignatures(t *testing.T) {
	payload := `{"eventType":"test"}`
	signed := fmt.Sprintf("t=%d,v1=%s,v1=%s", nowEpoch, engineSign(nowEpoch, payload, "old_secret"), engineSign(nowEpoch, payload, secret))
	te.Equal(t, te.Must(ConstructEvent([]byte(payload), signed, secret, nil)).EventType, "test")
}

func TestToleratesSpacesAroundTheParts(t *testing.T) {
	payload := `{"eventType":"test"}`
	signed := fmt.Sprintf(" t=%d , v1=%s ", nowEpoch, engineSign(nowEpoch, payload, secret))
	te.Must(ConstructEvent([]byte(payload), signed, secret, nil))
}

func TestRefusesTheWrongSecretAWrongLengthSignatureAndATamperedBody(t *testing.T) {
	payload := `{"eventType":"test","amount":1}`
	_, err := ConstructEvent([]byte(payload), fmt.Sprintf("t=%d,v1=%s", nowEpoch, engineSign(nowEpoch, payload, "wrong")), secret, nil)
	te.Equal(t, te.As[*SignatureError](t, err).Error(), "No matching signature found.")
	_, err = ConstructEvent([]byte(payload), fmt.Sprintf("t=%d,v1=%s", nowEpoch, engineSign(nowEpoch, payload, secret)[:10]), secret, nil)
	te.As[*SignatureError](t, err)
	_, err = ConstructEvent([]byte(strings.Replace(payload, "1", "9", 1)), header(payload, nowEpoch), secret, nil)
	te.As[*SignatureError](t, err)
}

func TestDecodesEveryKnownEventIntoItsOwnType(t *testing.T) {
	expected := map[string]EventData{
		"resource.created":       &ResourceCreatedData{},
		"resource.taken":         &ResourceTakenData{},
		"resource.confirmed":     &ResourceConfirmedData{},
		"resource.released":      &ResourceReleasedData{},
		"resource.extended":      &ResourceExtendedData{},
		"resource.expired":       &ResourceExpiredData{},
		"resource.updated":       &ResourceUpdatedData{},
		"resource.deleted":       &ResourceDeletedData{},
		"resource.queued":        &ResourceQueuedData{},
		"resource.take_failed":   &ResourceTakeFailedData{},
		"lock.acquired":          &LockAcquiredData{},
		"lock.released":          &LockReleasedData{},
		"lock.acquire_failed":    &LockAcquireFailedData{},
		"lock.deadlock_detected": &DeadlockDetectedData{},
		"lock.abandoned":         &LockAbandonedData{},
		"transaction.started":    &TransactionStartedData{},
		"transaction.completed":  &TransactionCompletedData{},
		"transaction.aborted":    &TransactionAbortedData{},
		"transaction.renewed":    &TransactionRenewedData{},
		"transaction.expired":    &TransactionExpiredData{},
	}
	for eventType, want := range expected {
		event := construct(t, fmt.Sprintf(`{"eventType":%q,"data":{"eventId":"e1"}}`, eventType))
		te.Equal(t, reflect.TypeOf(event.Data), reflect.TypeOf(want))
		te.Equal(t, event.Data.Meta().EventID, "e1")
	}
}

func TestReadsTheFieldsOfEachEvent(t *testing.T) {
	updated := construct(t, `{"eventType":"resource.updated","data":{"resourceKey":"g","deltaAmount":-2,"updatedAtMs":"17"}}`).Data.(*ResourceUpdatedData)
	te.Equal(t, *updated.DeltaAmount, -2)
	te.Equal(t, updated.UpdatedAtMs, int64(17))
	te.True(t, construct(t, `{"eventType":"resource.updated","data":{}}`).Data.(*ResourceUpdatedData).DeltaAmount == nil, "a missing delta must stay nil")
	started := construct(t, `{"eventType":"transaction.started","data":{"transactionId":"tx-1","transactionTimeoutMs":30000,"apiKeyId":"k1","actorId":"a1","environmentId":"env"}}`).Data.(*TransactionStartedData)
	te.Equal(t, *started, TransactionStartedData{EventMeta: EventMeta{EnvironmentID: "env", APIKeyID: "k1", ActorID: "a1"}, TransactionID: "tx-1", TransactionTimeoutMs: 30000})
	abandoned := construct(t, `{"eventType":"lock.abandoned","data":{"transactionId":"tx","lockIds":["l1",null,"l2"],"reason":"EXPIRED"}}`).Data.(*LockAbandonedData)
	te.Equal(t, abandoned.LockIDs, []string{"l1", "l2"})
	te.Equal(t, abandoned.Reason, "EXPIRED")
	created := construct(t, `{"eventType":"resource.created","data":{"metadata":{"zona":"platea"},"availableAmount":2.9}}`).Data.(*ResourceCreatedData)
	te.Equal(t, created.Metadata, `{"zona":"platea"}`)
	te.Equal(t, created.AvailableAmount, 2)
}

func TestReadsTheDeadlockCycle(t *testing.T) {
	data := construct(t, `{"eventType":"lock.deadlock_detected","data":{"victimTransactionId":"tx-2","cycleTransactionIds":["tx-1","tx-2"],"resolutionStrategy":"ABORT"}}`).Data.(*DeadlockDetectedData)
	te.Equal(t, data.VictimTransactionID, "tx-2")
	te.Equal(t, data.CycleTransactionIDs, []string{"tx-1", "tx-2"})
	te.Equal(t, data.ResolutionStrategy, "ABORT")
	te.Equal(t, data.Reason, "")
}

func TestReadsAFencingTokenBeyondTheFloatPrecision(t *testing.T) {
	data := construct(t, `{"eventType":"lock.acquired","data":{"fencingToken":9007199254740993,"mode":"EXCLUSIVE"}}`).Data.(*LockAcquiredData)
	te.Equal(t, data.FencingToken, int64(9_007_199_254_740_993))
	te.Equal(t, data.Mode, "EXCLUSIVE")
}

func TestAnEventTheSDKDoesNotKnowYetDoesNotBreakTheConsumer(t *testing.T) {
	data, ok := construct(t, `{"eventType":"queue.something_new","data":{"eventId":"e9","whatever":[1,2]}}`).Data.(*UnknownEventData)
	te.True(t, ok, "expected UnknownEventData")
	te.Equal(t, data.EventID, "e9")
	te.Equal(t, data.Fields["whatever"], any([]any{float64(1), float64(2)}))
	te.Equal(t, construct(t, `{"data":{"eventId":"e10"}}`).Data.Meta().EventID, "e10")
	te.Equal(t, len(construct(t, `{"eventType":"nuevo"}`).Data.(*UnknownEventData).Fields), 0)
}

func TestKeepsTheWholeEnvelope(t *testing.T) {
	te.Equal(t, construct(t, `{"eventType":"test","futureField":true}`).Raw["futureField"], any(true))
}

func TestASignedBodyThatIsNotJSONOrNotAnObjectIsAPayloadError(t *testing.T) {
	for _, body := range []string{"esto no es json", `{"a":1} trailing`} {
		_, err := ConstructEvent([]byte(body), header(body, nowEpoch), secret, nil)
		payloadErr := te.As[*PayloadError](t, err)
		te.Equal(t, payloadErr.Error(), "The webhook signature is valid but the body is not valid JSON.")
		te.True(t, errors.Unwrap(payloadErr.Unwrap()) != nil, "the parse error should stay reachable")
		te.Equal(t, payloadErr.Code(), caerus.CodeValidation)
	}
	_, err := ConstructEvent([]byte("[1,2,3]"), header("[1,2,3]", nowEpoch), secret, nil)
	te.Contains(t, te.As[*PayloadError](t, err).Error(), "not a JSON object")
}

func TestEveryWebhookErrorIsAValidationCaerusError(t *testing.T) {
	failures := []error{}
	for _, attempt := range []func() error{
		func() error { _, err := ConstructEvent([]byte("{}"), "", secret, nil); return err },
		func() error { _, err := ConstructEvent([]byte("{}"), "sin formato", secret, nil); return err },
		func() error {
			_, err := ConstructEvent([]byte("{}"), header("{}", nowEpoch-1000), secret, nil)
			return err
		},
		func() error { _, err := ConstructEvent([]byte("{}"), header("{}", nowEpoch), "", nil); return err },
		func() error {
			_, err := ConstructEvent([]byte("nope"), header("nope", nowEpoch), secret, nil)
			return err
		},
	} {
		failures = append(failures, attempt())
	}
	for _, err := range failures {
		te.Equal(t, te.As[*caerus.Error](t, err).Code(), caerus.CodeValidation)
	}
}

const (
	engineHex     = "5e3b7a684499bb93c4402adc4c4c6914c9c19be915d55179ba6b881175fe8775"
	interopTS     = int64(1_724_425_200)
	interopBody   = `{"event_id":"123","eventType":"resource.taken"}`
	interopSecret = "whsec_test_secret_key_123456789"
)

var noExpiry = &ConstructEventOptions{Tolerance: 1_000_000_000 * time.Second}

func TestSignsExactlyTheSameAsTheEngineAndTheTypeScriptSDK(t *testing.T) {
	te.Equal(t, sign(interopSecret, fmt.Sprint(interopTS), []byte(interopBody)), engineHex)
	te.Equal(t, engineSign(interopTS, interopBody, interopSecret), engineHex)
}

func TestAcceptsASignatureGeneratedByTheEngine(t *testing.T) {
	event := te.Must(ConstructEvent([]byte(interopBody), fmt.Sprintf("t=%d,v1=%s", interopTS, engineHex), interopSecret, noExpiry))
	te.Equal(t, event.Raw["event_id"], any("123"))
	te.Equal(t, event.EventType, "resource.taken")
	_, ok := event.Data.(*ResourceTakenData)
	te.True(t, ok, "got %T", event.Data)
}

func TestRefusesAnInteropSignatureThatDoesNotMatchOrIsTooOld(t *testing.T) {
	_, err := ConstructEvent([]byte(interopBody), fmt.Sprintf("t=%d,v1=%s", interopTS, strings.Repeat("0", 64)), interopSecret, noExpiry)
	te.As[*SignatureError](t, err)
	_, err = ConstructEvent([]byte(interopBody), fmt.Sprintf("t=%d,v1=%s", interopTS, engineHex), interopSecret, nil)
	te.As[*ExpiredError](t, err)
}

func TestRefusesAnEmptySecretInsteadOfVerifyingAgainstNothing(t *testing.T) {
	for _, empty := range []string{"", "   "} {
		_, err := ConstructEvent([]byte(interopBody), fmt.Sprintf("t=%d,v1=%s", interopTS, engineSign(interopTS, interopBody, "")), empty, noExpiry)
		te.As[*SignatureError](t, err)
	}
}
