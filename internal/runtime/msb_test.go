package runtime

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestSecretsFromSpec(t *testing.T) {
	requireTLS := false
	ns := sandbox.NetworkSpec{
		Enabled: true,
		Secrets: &sandbox.SecretsConfig{
			OnViolation: &sandbox.SecretViolationAction{Type: "block_and_terminate"},
			Entries: []sandbox.SecretEntry{{
				EnvVar:      "GITHUB_TOKEN",
				Value:       "ghs_secret",
				Placeholder: "MSB_GITHUB_TOKEN",
				AllowedHosts: []sandbox.SecretHostPattern{
					{Type: "exact", Value: "github.com"},
					{Type: "exact", Value: "api.github.com"},
					{Type: "wildcard", Value: "*.githubusercontent.com"},
				},
				RequireTLSIdentity: &requireTLS,
				OnViolation:        &sandbox.SecretViolationAction{Type: "block"},
			}},
		},
	}
	got := secretsFromSpec(ns)
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	s := got[0]
	if s.EnvVar != "GITHUB_TOKEN" || s.Value != "ghs_secret" || s.Placeholder != "MSB_GITHUB_TOKEN" {
		t.Fatalf("secret=%#v", s)
	}
	if len(s.AllowHosts) != 2 || s.AllowHosts[0] != "github.com" || s.AllowHosts[1] != "api.github.com" {
		t.Fatalf("AllowHosts=%v", s.AllowHosts)
	}
	if len(s.AllowHostPatterns) != 1 || s.AllowHostPatterns[0] != "*.githubusercontent.com" {
		t.Fatalf("AllowHostPatterns=%v", s.AllowHostPatterns)
	}
	if s.RequireTLS == nil || *s.RequireTLS {
		t.Fatalf("RequireTLS=%v", s.RequireTLS)
	}
	if s.OnViolation != msb.ViolationActionBlock {
		t.Fatalf("OnViolation=%q", s.OnViolation)
	}

	net := networkFromSpec(ns)
	if net == nil {
		t.Fatal("networkFromSpec nil")
	}
	if net.OnSecretViolation != msb.ViolationActionBlockAndTerminate {
		t.Fatalf("OnSecretViolation=%q", net.OnSecretViolation)
	}
}

func TestSpecToOptionsWithSecrets(t *testing.T) {
	spec := sandbox.Spec{
		Name:      "demo",
		Image:     sandbox.OCIImage("alpine:3.20"),
		Resources: sandbox.Resources{VCPUs: 1, MemoryMiB: 512},
		Network: sandbox.NetworkSpec{
			Enabled: true,
			Secrets: &sandbox.SecretsConfig{
				Entries: []sandbox.SecretEntry{{
					EnvVar:       "TOK",
					Value:        "secret-value",
					Placeholder:  "MSB_TOK",
					AllowedHosts: []sandbox.SecretHostPattern{{Type: "exact", Value: "api.example.com"}},
				}},
			},
		},
	}
	opts, err := specToOptions(spec)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &msb.SandboxConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if len(cfg.Secrets) != 1 {
		t.Fatalf("Secrets=%#v", cfg.Secrets)
	}
	if cfg.Secrets[0].EnvVar != "TOK" || cfg.Secrets[0].Value != "secret-value" {
		t.Fatalf("secret=%#v", cfg.Secrets[0])
	}
	if cfg.Secrets[0].Placeholder != "MSB_TOK" {
		t.Fatalf("placeholder=%q", cfg.Secrets[0].Placeholder)
	}
	if len(cfg.Secrets[0].AllowHosts) != 1 || cfg.Secrets[0].AllowHosts[0] != "api.example.com" {
		t.Fatalf("AllowHosts=%v", cfg.Secrets[0].AllowHosts)
	}
	if cfg.Network == nil {
		t.Fatal("Network nil")
	}
}
