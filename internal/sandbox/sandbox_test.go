package sandbox_test

import (
	"strings"
	"testing"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestValidateSpec(t *testing.T) {
	err := sandbox.ValidateSpec(sandbox.Spec{})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name required, got %v", err)
	}
	err = sandbox.ValidateSpec(sandbox.Spec{
		Name:      "demo",
		Image:     sandbox.OCIImage("alpine:3.20"),
		Resources: sandbox.Resources{VCPUs: 1, MemoryMiB: 512},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeSpec(t *testing.T) {
	s := sandbox.NormalizeSpec(sandbox.Spec{
		Name:  "demo",
		Image: sandbox.RootfsSource{Reference: "busybox"},
	})
	if s.Image.Type != "oci" {
		t.Fatalf("type=%q", s.Image.Type)
	}
	if s.Resources.VCPUs != 1 || s.Resources.MemoryMiB != 512 {
		t.Fatalf("resources=%+v", s.Resources)
	}
	if s.Slug != "demo" {
		t.Fatalf("slug=%q", s.Slug)
	}
}

func TestHasHostMounts(t *testing.T) {
	s := sandbox.Spec{Mounts: []sandbox.VolumeMount{{Type: "named", Name: "v", Guest: "/data"}}}
	if s.HasHostMounts() {
		t.Fatal("named should not pin host mounts")
	}
	s.Mounts = []sandbox.VolumeMount{{Type: "bind", Host: "/tmp", Guest: "/data"}}
	if !s.HasHostMounts() {
		t.Fatal("bind should pin")
	}
}

func TestSpecFromJSONSecrets(t *testing.T) {
	raw := `{
		"name":"secret-sub-check",
		"image":{"type":"oci","reference":"buildpack-deps:bookworm"},
		"resources":{"vcpus":1,"memory_mib":1024},
		"runtime":{},
		"network":{
			"enabled":true,
			"secrets":{
				"entries":[{
					"env_var":"GITHUB_TOKEN",
					"value":"ghs_secret",
					"placeholder":"MSB_GITHUB_TOKEN",
					"allowed_hosts":[
						{"type":"exact","value":"github.com"},
						{"type":"exact","value":"api.github.com"}
					],
					"require_tls_identity":true
				}],
				"on_violation":{"type":"block_and_log"}
			}
		},
		"lifecycle":{}
	}`
	spec, err := sandbox.SpecFromJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Network.Secrets == nil || len(spec.Network.Secrets.Entries) != 1 {
		t.Fatalf("secrets=%#v", spec.Network.Secrets)
	}
	e := spec.Network.Secrets.Entries[0]
	if e.EnvVar != "GITHUB_TOKEN" || e.Value != "ghs_secret" || e.Placeholder != "MSB_GITHUB_TOKEN" {
		t.Fatalf("entry=%#v", e)
	}
	if len(e.AllowedHosts) != 2 || e.AllowedHosts[0].Type != "exact" || e.AllowedHosts[0].Value != "github.com" {
		t.Fatalf("hosts=%#v", e.AllowedHosts)
	}
	if !e.RequireTLSIdentityEffective() {
		t.Fatal("require_tls_identity should default/effective true")
	}
	if err := sandbox.ValidateSpec(spec); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSpecSecrets(t *testing.T) {
	base := sandbox.Spec{
		Name:      "demo",
		Image:     sandbox.OCIImage("alpine:3.20"),
		Resources: sandbox.Resources{VCPUs: 1, MemoryMiB: 512},
		Network: sandbox.NetworkSpec{
			Enabled: false,
			Secrets: &sandbox.SecretsConfig{
				Entries: []sandbox.SecretEntry{{
					EnvVar:       "TOK",
					Value:        "v",
					Placeholder:  "P",
					AllowedHosts: []sandbox.SecretHostPattern{{Type: "exact", Value: "api.example.com"}},
				}},
			},
		},
	}
	err := sandbox.ValidateSpec(base)
	if err == nil || !strings.Contains(err.Error(), "network.enabled") {
		t.Fatalf("expected enabled required, got %v", err)
	}

	base.Network.Enabled = true
	base.Network.Secrets.Entries[0].Value = ""
	err = sandbox.ValidateSpec(base)
	if err == nil || !strings.Contains(err.Error(), "value is required") {
		t.Fatalf("expected value required, got %v", err)
	}

	base.Network.Secrets.Entries[0].Value = ""
	base.Network.Secrets.Entries[0].Source = []byte(`{"type":"env","var":"HOST_TOK"}`)
	err = sandbox.ValidateSpec(base)
	if err == nil || !strings.Contains(err.Error(), "source-only") {
		t.Fatalf("expected source-only rejection, got %v", err)
	}

	base.Network.Secrets.Entries[0].Source = nil
	base.Network.Secrets.Entries[0].Value = "secret"
	base.Network.Secrets.Entries[0].AllowedHosts = nil
	err = sandbox.ValidateSpec(base)
	if err == nil || !strings.Contains(err.Error(), "allowed_hosts") {
		t.Fatalf("expected allowed_hosts required, got %v", err)
	}
}

func TestSpecToJSONRedacted(t *testing.T) {
	spec := sandbox.Spec{
		Name:  "demo",
		Image: sandbox.OCIImage("alpine:3.20"),
		Network: sandbox.NetworkSpec{
			Enabled: true,
			Secrets: &sandbox.SecretsConfig{
				Entries: []sandbox.SecretEntry{{
					EnvVar:       "TOK",
					Value:        "super-secret",
					Placeholder:  "PH",
					AllowedHosts: []sandbox.SecretHostPattern{{Type: "exact", Value: "api.example.com"}},
				}},
			},
		},
	}
	raw, err := sandbox.SpecToJSONRedacted(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret") {
		t.Fatalf("secret value leaked in redacted JSON: %s", raw)
	}
	full, err := sandbox.SpecToJSON(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(full), "super-secret") {
		t.Fatal("SpecToJSON should keep secret values for cluster gRPC")
	}
	if spec.Network.Secrets.Entries[0].Value != "super-secret" {
		t.Fatal("SpecToJSONRedacted mutated the original spec")
	}
}
