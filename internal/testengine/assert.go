package testengine

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func Equal[T any](t testing.TB, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func True(t testing.TB, condition bool, message string, args ...any) {
	t.Helper()
	if !condition {
		t.Fatalf(message, args...)
	}
}

func NoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func Must[T any](value T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}
	return value
}

func As[T error](t testing.TB, err error) T {
	t.Helper()
	var target T
	if !errors.As(err, &target) {
		t.Fatalf("expected %T, got %T: %v", target, err, err)
	}
	return target
}

func NotAs[T error](t testing.TB, err error) {
	t.Helper()
	var target T
	if errors.As(err, &target) {
		t.Fatalf("did not expect %T, got %v", target, err)
	}
}

func Contains(t testing.TB, text, part string) {
	t.Helper()
	if !strings.Contains(text, part) {
		t.Fatalf("%q does not contain %q", text, part)
	}
}

func NotContains(t testing.TB, text, part string) {
	t.Helper()
	if strings.Contains(text, part) {
		t.Fatalf("%q should not contain %q", text, part)
	}
}
