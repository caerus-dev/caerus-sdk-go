package dls

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/config"
	"github.com/caerus-dev/caerus-sdk-go/internal/logging"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

func resolve(t *testing.T, options ClientOptions, environment map[string]string) config.Settings {
	t.Helper()
	return te.Must(config.Resolve(config.DLS, config.Input{
		APIKey: options.APIKey, Endpoint: options.Endpoint, TLS: options.TLS, Timeout: options.Timeout, Logger: options.Logger,
	}, env(environment)))
}

func TestRefusesAMissingOrBlankAPIKey(t *testing.T) {
	for _, key := range []string{"", "  "} {
		_, err := newClient(ClientOptions{APIKey: key}, noEnv)
		te.True(t, err != nil, "expected an error")
		te.Contains(t, err.Error(), "dls.Client requires an APIKey")
	}
}

func TestKeepsTheAPIKeyOutOfEverythingThatGetsPrinted(t *testing.T) {
	options := ClientOptions{APIKey: "sk_dls_secret", Endpoint: "localhost:1", TLS: caerus.Bool(false)}
	client := te.Must(newClient(options, noEnv))
	defer client.Close()
	for _, printed := range []string{fmt.Sprint(options), fmt.Sprintf("%+v", options), fmt.Sprintf("%#v", options), fmt.Sprint(client), fmt.Sprintf("%+v", client)} {
		te.NotContains(t, printed, "sk_dls_secret")
	}
}

func TestNeedsNothingButAnAPIKeyEncryptsAndPutsADeadlineByDefault(t *testing.T) {
	settings := resolve(t, ClientOptions{APIKey: "sk"}, nil)
	te.Equal(t, settings.Endpoint, caerus.DefaultEndpoint)
	te.Equal(t, settings.TLS, true)
	te.Equal(t, settings.Timeout, 10*time.Second)
}

func TestReadsItsOwnVariablesNotTheSREOnes(t *testing.T) {
	settings := resolve(t, ClientOptions{APIKey: "sk"}, map[string]string{"CAERUS_ENDPOINT": "sre:1", "CAERUS_TLS": "false"})
	te.Equal(t, settings.Endpoint, caerus.DefaultEndpoint)
	te.Equal(t, settings.TLS, true)
	settings = resolve(t, ClientOptions{APIKey: "sk"}, map[string]string{"CAERUS_DLS_ENDPOINT": "dls:1", "CAERUS_DLS_TLS": "0"})
	te.Equal(t, settings.Endpoint, "dls:1")
	te.Equal(t, settings.TLS, false)
}

func TestPrefersAnExplicitEndpointAndTLSOverTheEnvironment(t *testing.T) {
	settings := resolve(t, ClientOptions{APIKey: "sk", Endpoint: "explicit:2", TLS: caerus.Bool(true)}, map[string]string{"CAERUS_DLS_ENDPOINT": "dls:1", "CAERUS_DLS_TLS": "false"})
	te.Equal(t, settings.Endpoint, "explicit:2")
	te.Equal(t, settings.TLS, true)
}

func TestRefusesAnExplicitBlankEndpoint(t *testing.T) {
	_, err := newClient(ClientOptions{APIKey: "sk", Endpoint: " "}, noEnv)
	te.True(t, err != nil, "expected an error")
}

func TestLogsWithItsOwnPrefix(t *testing.T) {
	te.Equal(t, config.DLS.LoggerPrefix, "[caerus-dls]")
	var out bytes.Buffer
	logging.NewTo(config.DLS.LoggerPrefix, &out).Error("onQueued callback threw", "panic", "boom")
	te.Equal(t, out.String(), "[caerus-dls] onQueued callback threw panic=boom\n")
}
