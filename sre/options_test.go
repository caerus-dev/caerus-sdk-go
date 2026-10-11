package sre

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
	return te.Must(config.Resolve(config.SRE, config.Input{
		APIKey: options.APIKey, Endpoint: options.Endpoint, TLS: options.TLS, Timeout: options.Timeout, Logger: options.Logger,
	}, env(environment)))
}

func TestRefusesAMissingAPIKeyAndSaysWhatIsMissing(t *testing.T) {
	_, err := newClient(ClientOptions{}, noEnv)
	te.True(t, err != nil, "expected an error")
	te.Contains(t, err.Error(), "requires an APIKey")
	te.Contains(t, err.Error(), "Caerus dashboard")
}

func TestRefusesABlankAPIKey(t *testing.T) {
	_, err := newClient(ClientOptions{APIKey: "   "}, noEnv)
	te.True(t, err != nil, "expected an error")
}

func TestTrimsTheAPIKey(t *testing.T) {
	te.Equal(t, resolve(t, ClientOptions{APIKey: "  sk_x  "}, nil).APIKey, "sk_x")
}

func TestKeepsTheAPIKeyOutOfEverythingThatGetsPrinted(t *testing.T) {
	options := ClientOptions{APIKey: "sk_secret_value", Endpoint: "localhost:1", TLS: caerus.Bool(false)}
	client := te.Must(newClient(options, noEnv))
	defer client.Close()
	for _, printed := range []string{
		fmt.Sprint(options), fmt.Sprintf("%+v", options), fmt.Sprintf("%#v", options),
		fmt.Sprint(client), fmt.Sprintf("%+v", client), resolve(t, options, nil).String(),
	} {
		te.NotContains(t, printed, "sk_secret_value")
	}
}

func TestNeedsNothingButAnAPIKeyToReachTheHostedCaerus(t *testing.T) {
	settings := resolve(t, ClientOptions{APIKey: "sk"}, nil)
	te.Equal(t, settings.Endpoint, caerus.DefaultEndpoint)
	te.Equal(t, settings.Endpoint, "caerus.dev.ar.sdk.apps.disilab.ar:443")
}

func TestEncryptsTheConnectionUnlessToldOtherwise(t *testing.T) {
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk"}, nil).TLS, true)
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk"}, map[string]string{"CAERUS_TLS": ""}).TLS, true)
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk"}, map[string]string{"CAERUS_TLS": "no"}).TLS, true)
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk"}, map[string]string{"CAERUS_TLS": "FALSE"}).TLS, false)
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk"}, map[string]string{"CAERUS_TLS": " 0 "}).TLS, false)
}

func TestPutsADeadlineOnCallsEvenWhenNoneIsAskedFor(t *testing.T) {
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk"}, nil).Timeout, 10*time.Second)
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk", Timeout: time.Second}, nil).Timeout, time.Second)
	_, err := config.Resolve(config.SRE, config.Input{APIKey: "sk", Timeout: -time.Second}, noEnv)
	te.True(t, err != nil, "a negative timeout must be refused")
}

func TestPrefersAnExplicitEndpointThenTheEnvironmentThenTheBuiltInOne(t *testing.T) {
	environment := map[string]string{"CAERUS_ENDPOINT": "from-env:443"}
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk", Endpoint: "explicit:1"}, environment).Endpoint, "explicit:1")
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk"}, environment).Endpoint, "from-env:443")
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk"}, nil).Endpoint, caerus.DefaultEndpoint)
}

func TestIgnoresABlankEndpointInTheEnvironment(t *testing.T) {
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk"}, map[string]string{"CAERUS_ENDPOINT": "   "}).Endpoint, caerus.DefaultEndpoint)
}

func TestRefusesAnExplicitBlankEndpoint(t *testing.T) {
	_, err := newClient(ClientOptions{APIKey: "sk", Endpoint: "   "}, noEnv)
	te.True(t, err != nil, "expected an error")
}

func TestHonorsEnvironmentOverridesForEndpointAndTLS(t *testing.T) {
	settings := resolve(t, ClientOptions{APIKey: "sk"}, map[string]string{"CAERUS_ENDPOINT": "localhost:9090", "CAERUS_TLS": "false"})
	te.Equal(t, settings.Endpoint, "localhost:9090")
	te.Equal(t, settings.TLS, false)
}

func TestAnExplicitTLSOptionWinsOverTheEnvironment(t *testing.T) {
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk", TLS: caerus.Bool(true)}, map[string]string{"CAERUS_TLS": "false"}).TLS, true)
	te.Equal(t, resolve(t, ClientOptions{APIKey: "sk", TLS: caerus.Bool(false)}, map[string]string{"CAERUS_TLS": "true"}).TLS, false)
}

func TestIgnoresTheDLSVariables(t *testing.T) {
	settings := resolve(t, ClientOptions{APIKey: "sk"}, map[string]string{"CAERUS_DLS_ENDPOINT": "dls:1", "CAERUS_DLS_TLS": "false"})
	te.Equal(t, settings.Endpoint, caerus.DefaultEndpoint)
	te.Equal(t, settings.TLS, true)
}

func TestLogsWithItsOwnPrefixByDefault(t *testing.T) {
	te.Equal(t, config.SRE.LoggerPrefix, "[caerus]")
	var out bytes.Buffer
	logger := logging.NewTo("[caerus]", &out)
	logger.Error("algo", "detalle", 1)
	te.Equal(t, out.String(), "[caerus] algo detalle=1\n")
}

func TestReadsTheEnvironmentOfTheProcessWhenBuiltNormally(t *testing.T) {
	t.Setenv("CAERUS_ENDPOINT", "127.0.0.1:1")
	t.Setenv("CAERUS_TLS", "false")
	client := te.Must(NewClient(ClientOptions{APIKey: "sk"}))
	defer client.Close()
	te.Equal(t, client.Endpoint(), "127.0.0.1:1")
	te.Equal(t, client.Timeout(), caerus.DefaultTimeout)
}
