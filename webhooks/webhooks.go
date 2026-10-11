package webhooks

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/jsonx"
)

const (
	SignatureHeader  = "Caerus-Signature"
	DefaultTolerance = 300 * time.Second
)

var now = time.Now

type ConstructEventOptions struct {
	Tolerance time.Duration
}

type Event struct {
	ID            string
	EventID       string
	Product       string
	ObjectType    string
	ObjectID      string
	EnvironmentID string
	OccurredAt    string
	EventType     string
	Data          EventData
	Raw           map[string]any
}

func ConstructEvent(payload []byte, signatureHeader, secret string, options *ConstructEventOptions) (*Event, error) {
	tolerance := DefaultTolerance
	if options != nil && options.Tolerance != 0 {
		tolerance = options.Tolerance
	}
	if signatureHeader == "" {
		return nil, NewSignatureError("No signature header provided.", nil)
	}
	if strings.TrimSpace(secret) == "" {
		return nil, NewSignatureError("No signing secret provided: verifying against an empty secret would accept forged events.", nil)
	}
	timestampText, hasTimestamp := "", false
	var signatures []string
	for _, part := range strings.Split(signatureHeader, ",") {
		trimmed := strings.TrimSpace(part)
		if !hasTimestamp && strings.HasPrefix(trimmed, "t=") {
			timestampText, hasTimestamp = trimmed[2:], true
		}
		if strings.HasPrefix(trimmed, "v1=") {
			signatures = append(signatures, trimmed[3:])
		}
	}
	if timestampText == "" || len(signatures) == 0 {
		return nil, NewSignatureError("Invalid signature format.", nil)
	}
	timestamp, ok := parseLeadingInteger(timestampText)
	if !ok {
		return nil, NewSignatureError("Invalid signature format.", nil)
	}
	elapsed := now().Unix() - timestamp
	if elapsed < 0 {
		elapsed = -elapsed
	}
	if elapsed > int64(tolerance/time.Second) {
		return nil, NewExpiredError("Webhook timestamp is outside of the tolerance zone.", nil)
	}
	expected := []byte(sign(secret, timestampText, payload))
	valid := false
	for _, signature := range signatures {
		if hmac.Equal([]byte(signature), expected) {
			valid = true
		}
	}
	if !valid {
		return nil, NewSignatureError("No matching signature found.", nil)
	}
	if !utf8.Valid(payload) {
		return nil, NewPayloadError("The webhook signature is valid but the body is not valid UTF-8.", nil)
	}
	raw, err := jsonx.Parse(payload)
	if err != nil {
		return nil, NewPayloadError("The webhook signature is valid but the body is not valid JSON.", &caerus.ErrorOptions{Cause: err})
	}
	envelope, isObject := raw.(map[string]any)
	if !isObject {
		return nil, NewPayloadError("The webhook signature is valid but the body is not a JSON object.", nil)
	}
	precise, err := parsePrecise(payload)
	if err != nil {
		return nil, NewPayloadError("The webhook signature is valid but the body is not valid JSON.", &caerus.ErrorOptions{Cause: err})
	}
	return toEvent(envelope, precise), nil
}

func sign(secret, timestamp string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func toEvent(envelope map[string]any, precise map[string]any) *Event {
	top := fields(precise)
	data, _ := precise["data"].(map[string]any)
	rawData, _ := envelope["data"].(map[string]any)
	eventType := top.str("eventType")
	return &Event{
		ID:            top.str("id"),
		EventID:       top.str("eventId"),
		Product:       top.str("product"),
		ObjectType:    top.str("objectType"),
		ObjectID:      top.str("objectId"),
		EnvironmentID: top.str("environmentId"),
		OccurredAt:    top.str("occurredAt"),
		EventType:     eventType,
		Data:          decode(eventType, fields(data), rawData),
		Raw:           envelope,
	}
}

func parsePrecise(payload []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	return object, nil
}

func parseLeadingInteger(text string) (int64, bool) {
	index := 0
	for index < len(text) && isSpace(text[index]) {
		index++
	}
	negative := false
	if index < len(text) && (text[index] == '+' || text[index] == '-') {
		negative = text[index] == '-'
		index++
	}
	start := index
	var value int64
	for index < len(text) && text[index] >= '0' && text[index] <= '9' {
		if index-start >= 18 {
			return 0, false
		}
		value = value*10 + int64(text[index]-'0')
		index++
	}
	if index == start {
		return 0, false
	}
	if negative {
		return -value, true
	}
	return value, true
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}
