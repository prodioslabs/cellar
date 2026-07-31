package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareSandboxDir(t *testing.T) {
	dir := t.TempDir()
	token, err := PrepareSandboxDir(dir, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatalf("token len: got %d", len(token))
	}
	got, err := ReadAgentToken(dir, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Fatalf("token mismatch")
	}
	st, err := os.Stat(AgentTokenPath(dir, "abc123"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("token perms: %o", st.Mode().Perm())
	}
	dirSt, err := os.Stat(SandboxHostDir(dir, "abc123"))
	if err != nil {
		t.Fatal(err)
	}
	if dirSt.Mode().Perm() != 0o777 {
		t.Fatalf("sandbox dir perms: %o", dirSt.Mode().Perm())
	}
	parentSt, err := os.Stat(filepath.Join(dir, sandboxDirName))
	if err != nil {
		t.Fatal(err)
	}
	if parentSt.Mode().Perm() != 0o700 {
		t.Fatalf("sandboxes parent perms: %o", parentSt.Mode().Perm())
	}
	if err := CleanupSandboxDir(dir, "abc123"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SandboxHostDir(dir, "abc123")); !os.IsNotExist(err) {
		t.Fatalf("expected dir removed, err=%v", err)
	}
}

func TestWriteEgressResolvConf(t *testing.T) {
	dir := t.TempDir()
	if _, err := PrepareSandboxDir(dir, "abc123"); err != nil {
		t.Fatal(err)
	}
	path, err := WriteEgressResolvConf(dir, "abc123", "203.0.113.53")
	if err != nil {
		t.Fatal(err)
	}
	if path != ResolvConfPath(dir, "abc123") {
		t.Fatalf("path: got %q want %q", path, ResolvConfPath(dir, "abc123"))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "nameserver 203.0.113.53\noptions ndots:0\n"
	if string(b) != want {
		t.Fatalf("contents: got %q want %q", string(b), want)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("resolv.conf perms: %o", st.Mode().Perm())
	}
}

func TestResolveAgentBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "cellar-agent")
	if err := os.WriteFile(bin, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveAgentBinary(bin)
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		t.Fatalf("got %q want %q", got, bin)
	}
}
