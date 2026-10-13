package caerus

import "time"

const (
	Version         = "0.1.0"
	DefaultEndpoint = "caerus.dev.ar.sdk.apps.disilab.ar:443"
	DefaultTimeout  = 10 * time.Second
)

func Bool(value bool) *bool {
	return &value
}
