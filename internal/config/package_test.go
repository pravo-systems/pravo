package config

import (
	"testing"

	"pravo.dev/pravo/internal/registry"
)

func TestSourceOfPackage(t *testing.T) {
	for tail, want := range map[string]registry.Source{
		"hashicorp/aws":                  {Host: registry.DefaultHost, Namespace: "hashicorp", Type: "aws"},
		"example.com/acme/thing":         {Host: "example.com", Namespace: "acme", Type: "thing"},
		"oci/ghcr.io/acme/providers/aws": {Host: "ghcr.io", Namespace: "acme/providers", Type: "aws", OCI: true},
		"oci/localhost_5000/acme/random": {Host: "localhost:5000", Namespace: "acme", Type: "random", OCI: true},
	} {
		got, ok := sourceOfPackage(tail)
		if !ok || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", tail, got, ok, want)
		}
	}
}
