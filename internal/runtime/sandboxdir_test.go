package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareSandboxDir(t *testing.T) {
	dir := t.TempDir()
	if err := PrepareSandboxDir(dir, "abc123"); err != nil {
		t.Fatal(err)
	}
	dirSt, err := os.Stat(SandboxHostDir(dir, "abc123"))
	if err != nil {
		t.Fatal(err)
	}
	if dirSt.Mode().Perm() != 0o700 {
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
	if err := PrepareSandboxDir(dir, "abc123"); err != nil {
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

func TestStageAgentBinary(t *testing.T) {
	srcDir := t.TempDir()
	dataDir := t.TempDir()
	src := filepath.Join(srcDir, "cellar-agent")
	content := []byte("fake-agent-binary")
	if err := os.WriteFile(src, content, 0o755); err != nil {
		t.Fatal(err)
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}

	staged, err := StageAgentBinary(dataDir, src)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataDir, stagedAgentName)
	if staged != want {
		t.Fatalf("staged path: got %q want %q", staged, want)
	}
	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("staged contents: got %q want %q", got, content)
	}
	st, err := os.Stat(staged)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Fatalf("staged perms: %o", st.Mode().Perm())
	}
	if !st.ModTime().Equal(srcInfo.ModTime()) {
		t.Fatalf("staged mtime: got %v want %v", st.ModTime(), srcInfo.ModTime())
	}

	// Second call should reuse the staged copy without error.
	again, err := StageAgentBinary(dataDir, src)
	if err != nil {
		t.Fatal(err)
	}
	if again != staged {
		t.Fatalf("reuse path: got %q want %q", again, staged)
	}

	// Staging from an already-staged path is a no-op.
	same, err := StageAgentBinary(dataDir, staged)
	if err != nil {
		t.Fatal(err)
	}
	if same != staged {
		t.Fatalf("self-stage path: got %q want %q", same, staged)
	}
}

func TestStageAgentBinaryRefresh(t *testing.T) {
	srcDir := t.TempDir()
	dataDir := t.TempDir()
	src := filepath.Join(srcDir, "cellar-agent")
	if err := os.WriteFile(src, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := StageAgentBinary(dataDir, src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("v2-longer"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged, err := StageAgentBinary(dataDir, src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2-longer" {
		t.Fatalf("refreshed contents: got %q", got)
	}
}
