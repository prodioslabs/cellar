package runtime

import (
	"testing"
	"time"

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
	cfg := applyOpts(t, opts)
	if len(cfg.Secrets) != 1 {
		t.Fatalf("Secrets=%#v", cfg.Secrets)
	}
	if cfg.Secrets[0].EnvVar != "TOK" || cfg.Secrets[0].Value != "secret-value" {
		t.Fatalf("secret=%#v", cfg.Secrets[0])
	}
	if cfg.Network == nil {
		t.Fatal("Network nil")
	}
}

func TestSpecToOptionsCloudParity(t *testing.T) {
	shell := "/bin/bash"
	user := "root"
	level := "debug"
	maxDur := uint64(120)
	idle := uint64(30)
	uid, gid := uint32(1000), uint32(1000)
	quota := uint32(64)
	size := uint32(32)
	mode := uint32(0o644)

	spec := sandbox.Spec{
		Name: "demo",
		Image: sandbox.RootfsSource{
			Type: "oci", Reference: "alpine:3.20",
		},
		Resources: sandbox.Resources{VCPUs: 2, MemoryMiB: 1024, DiskSizeMiB: ptrU32(2048)},
		Runtime: sandbox.RuntimeOptions{
			Shell: &shell, User: &user, LogLevel: &level,
			Scripts: map[string]string{"hi": "echo hi"},
			Cmd:     []string{"sleep", "inf"},
		},
		SecurityProfile: sandbox.SecurityRestricted,
		Lifecycle: sandbox.LifecyclePolicy{
			Ephemeral: true, MaxDurationSecs: &maxDur, IdleTimeoutSecs: &idle,
		},
		Init: &sandbox.HandoffInit{
			Cmd: "/sbin/init",
			Env: []sandbox.EnvPair{{Key: "A", Value: "1"}},
		},
		Patches: []sandbox.Patch{
			{Type: "text", Path: "/etc/x", Content: &sandbox.PatchContent{Text: "x"}, Mode: &mode, Replace: true},
			{Type: "mkdir", Path: "/app"},
		},
		Mounts: []sandbox.VolumeMount{
			{
				Type: "bind", Host: "/host", Guest: "/data",
				Options: sandbox.MountOptions{
					Readonly: true, Noexec: true, OverrideUID: &uid, OverrideGID: &gid,
				},
				StatVirtualization: "relaxed",
				HostPermissions:    "mirror",
				QuotaMiB:           &quota,
			},
			{
				Type: "tmpfs", Guest: "/tmp", SizeMiB: &size,
				Options: sandbox.MountOptions{Noexec: true},
			},
			{
				Type: "disk_image", Host: "/disk.img", Guest: "/mnt", Format: "raw", Fstype: "ext4",
			},
		},
		Network:    sandbox.NetworkSpec{Enabled: true},
		PullPolicy: sandbox.PullAlways,
		Rlimits:    []sandbox.Rlimit{{Resource: "nofile", Soft: 1024, Hard: 2048}},
	}

	opts, err := specToOptions(spec)
	if err != nil {
		t.Fatal(err)
	}
	cfg := applyOpts(t, opts)

	if cfg.Image != "alpine:3.20" {
		t.Fatalf("Image=%q", cfg.Image)
	}
	if cfg.RootDisk == nil || cfg.RootDisk.SizeMiB != 2048 {
		t.Fatalf("RootDisk=%#v", cfg.RootDisk)
	}
	if cfg.Shell != "/bin/bash" || cfg.User != "root" || cfg.LogLevel != msb.LogLevelDebug {
		t.Fatalf("runtime shell/user/log=%q/%q/%q", cfg.Shell, cfg.User, cfg.LogLevel)
	}
	if cfg.Scripts["hi"] != "echo hi" {
		t.Fatalf("Scripts=%v", cfg.Scripts)
	}
	if cfg.SecurityProfile != msb.SecurityProfileRestricted {
		t.Fatalf("SecurityProfile=%q", cfg.SecurityProfile)
	}
	if !cfg.Ephemeral || cfg.MaxDuration != 120*time.Second || cfg.IdleTimeout != 30*time.Second {
		t.Fatalf("lifecycle ephemeral=%v max=%v idle=%v", cfg.Ephemeral, cfg.MaxDuration, cfg.IdleTimeout)
	}
	if cfg.Init == nil || cfg.Init.Cmd != "/sbin/init" || cfg.Init.Env["A"] != "1" {
		t.Fatalf("Init=%#v", cfg.Init)
	}
	if len(cfg.Patches) != 2 || cfg.Patches[0].Kind != msb.PatchKindText || cfg.Patches[1].Kind != msb.PatchKindMkdir {
		t.Fatalf("Patches=%#v", cfg.Patches)
	}
	bind := cfg.Volumes["/data"]
	if bind.Kind() != msb.MountKindBind || !bind.Readonly || !bind.Noexec {
		t.Fatalf("bind=%#v", bind)
	}
	if bind.Owner == nil || bind.Owner.UID != 1000 || bind.QuotaMiB != 64 {
		t.Fatalf("bind owner/quota=%#v", bind)
	}
	if bind.StatVirtualization != msb.StatVirtualizationRelaxed || bind.HostPermissions != msb.HostPermissionsMirror {
		t.Fatalf("bind virt/perm=%q/%q", bind.StatVirtualization, bind.HostPermissions)
	}
	if cfg.Volumes["/tmp"].Kind() != msb.MountKindTmpfs || cfg.Volumes["/tmp"].SizeMiB != 32 {
		t.Fatalf("tmpfs=%#v", cfg.Volumes["/tmp"])
	}
	disk := cfg.Volumes["/mnt"]
	if disk.Kind() != msb.MountKindDisk || disk.Disk != "/disk.img" || disk.Format != "raw" {
		t.Fatalf("disk=%#v", disk)
	}
	if cfg.PullPolicy != msb.PullPolicyAlways {
		t.Fatalf("PullPolicy=%q", cfg.PullPolicy)
	}
}

func TestSpecToOptionsBindRootfs(t *testing.T) {
	opts, err := specToOptions(sandbox.Spec{
		Name:      "demo",
		Image:     sandbox.RootfsSource{Type: "bind", Path: "/var/rootfs"},
		Resources: sandbox.Resources{VCPUs: 1, MemoryMiB: 512},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := applyOpts(t, opts)
	if cfg.ImageBind != "/var/rootfs" {
		t.Fatalf("ImageBind=%q", cfg.ImageBind)
	}
	if cfg.Image != "" {
		t.Fatalf("Image should be empty for bind rootfs, got %q", cfg.Image)
	}
}

func applyOpts(t *testing.T, opts []msb.SandboxOption) *msb.SandboxConfig {
	t.Helper()
	cfg := &msb.SandboxConfig{}
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

func ptrU32(v uint32) *uint32 { return &v }
