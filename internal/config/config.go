package config

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/logging"
)

type Profile struct {
	ClientName       string
	MissingAPIKey    string
	EndpointVariable string
	TLSVariable      string
	LoggerPrefix     string
}

var SRE = Profile{
	ClientName:       "sre.Client",
	MissingAPIKey:    "sre.Client requires an APIKey. Create one in the Caerus dashboard",
	EndpointVariable: "CAERUS_ENDPOINT",
	TLSVariable:      "CAERUS_TLS",
	LoggerPrefix:     "[caerus]",
}

var DLS = Profile{
	ClientName:       "dls.Client",
	MissingAPIKey:    "dls.Client requires an APIKey",
	EndpointVariable: "CAERUS_DLS_ENDPOINT",
	TLSVariable:      "CAERUS_DLS_TLS",
	LoggerPrefix:     "[caerus-dls]",
}

type Input struct {
	APIKey   string
	Endpoint string
	TLS      *bool
	Timeout  time.Duration
	Logger   *slog.Logger
}

type Settings struct {
	Endpoint string
	APIKey   string
	TLS      bool
	Timeout  time.Duration
	Logger   *slog.Logger
}

type Environment func(name string) (string, bool)

func System(name string) (string, bool) {
	return os.LookupEnv(name)
}

func Resolve(profile Profile, input Input, env Environment) (Settings, error) {
	if env == nil {
		env = System
	}
	key := strings.TrimSpace(input.APIKey)
	if key == "" {
		return Settings{}, errors.New(profile.MissingAPIKey)
	}
	endpoint := strings.TrimSpace(input.Endpoint)
	if input.Endpoint != "" && endpoint == "" {
		return Settings{}, errors.New("endpoint cannot be blank")
	}
	if endpoint == "" {
		fromEnv, _ := env(profile.EndpointVariable)
		endpoint = strings.TrimSpace(fromEnv)
	}
	if endpoint == "" {
		endpoint = caerus.DefaultEndpoint
	}
	timeout := input.Timeout
	if timeout == 0 {
		timeout = caerus.DefaultTimeout
	}
	if timeout < 0 {
		return Settings{}, errors.New("timeout must be a positive duration")
	}
	tls := true
	if input.TLS != nil {
		tls = *input.TLS
	} else if raw, ok := env(profile.TLSVariable); ok {
		value := strings.ToLower(strings.TrimSpace(raw))
		tls = !(value == "false" || value == "0")
	}
	logger := input.Logger
	if logger == nil {
		logger = logging.New(profile.LoggerPrefix)
	}
	return Settings{Endpoint: endpoint, APIKey: key, TLS: tls, Timeout: timeout, Logger: logger}, nil
}

func (s Settings) String() string {
	return "Settings{Endpoint: " + s.Endpoint + ", TLS: " + boolText(s.TLS) + ", Timeout: " + s.Timeout.String() + "}"
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
