//go:build live

package live

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/dls"
	"github.com/caerus-dev/caerus-sdk-go/sre"
)

var ctx = context.Background()

func env(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func apiKey(t *testing.T) string {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("CAERUS_API_KEY"))
	if key == "" {
		t.Skip("Falta CAERUS_API_KEY")
	}
	return key
}

func endpoint() string {
	return env("CAERUS_ENDPOINT", caerus.DefaultEndpoint)
}

func tls() *bool {
	raw := strings.ToLower(env("CAERUS_TLS", "true"))
	return caerus.Bool(!(raw == "false" || raw == "0"))
}

func dlsClient(t *testing.T, timeout time.Duration) *dls.Client {
	t.Helper()
	client, err := dls.NewClient(dls.ClientOptions{APIKey: apiKey(t), Endpoint: endpoint(), TLS: tls(), Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func sreClient(t *testing.T) *sre.Client {
	t.Helper()
	client, err := sre.NewClient(sre.ClientOptions{APIKey: apiKey(t), Endpoint: endpoint(), TLS: tls()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func key(name string) string {
	return fmt.Sprintf("verifgo_%d_%s", time.Now().UnixMilli(), name)
}

func report(t *testing.T, check string, detail any) {
	t.Helper()
	t.Logf("[en vivo] %s | %v", check, detail)
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}
	return value
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func check(t *testing.T, condition bool, format string, args ...any) {
	t.Helper()
	if !condition {
		t.Fatalf(format, args...)
	}
}
