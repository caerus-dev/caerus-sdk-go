package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func Stringify(value any) (string, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return string(bytes.TrimRight(out.Bytes(), "\n")), nil
}

func Parse(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected content after the JSON value")
	}
	return value, nil
}

func ParseObject(raw string) (map[string]any, error) {
	value, err := Parse([]byte(raw))
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	return object, nil
}
