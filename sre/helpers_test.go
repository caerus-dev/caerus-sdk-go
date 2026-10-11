package sre

import (
	"testing"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/config"
	"github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

func noEnv(string) (string, bool) {
	return "", false
}

func env(values map[string]string) config.Environment {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func connect(t *testing.T, engine *testengine.SRE, options ...func(*ClientOptions)) *Client {
	t.Helper()
	resolved := ClientOptions{APIKey: "sk_test_123", Endpoint: engine.Endpoint(), TLS: caerus.Bool(false)}
	for _, option := range options {
		option(&resolved)
	}
	client, err := newClient(resolved, noEnv)
	testengine.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}
