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
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("token perms: %o", st.Mode().Perm())
	}
	if err := CleanupSandboxDir(dir, "abc123"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SandboxHostDir(dir, "abc123")); !os.IsNotExist(err) {
		t.Fatalf("expected dir removed, err=%v", err)
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
