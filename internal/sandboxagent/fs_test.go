package sandboxagent

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAbsPath(t *testing.T) {
	if err := validateAbsPath(""); err == nil {
		t.Fatal("expected error for empty path")
	}
	if err := validateAbsPath("rel/path"); err == nil {
		t.Fatal("expected error for relative path")
	}
	if err := validateAbsPath("/abs\x00bad"); err == nil {
		t.Fatal("expected error for NUL")
	}
	if err := validateAbsPath("/tmp/ok"); err != nil {
		t.Fatal(err)
	}
}

func TestFsWriteReadStatList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")

	stdin := strings.NewReader("hello world")
	if err := withStdio(stdin, nil, func() error {
		return fsWrite([]string{path})
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	if err := withStdio(nil, &out, func() error {
		return fsRead([]string{path})
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := out.String(); got != "hello world" {
		t.Fatalf("read got %q", got)
	}

	out.Reset()
	if err := withStdio(nil, &out, func() error {
		return fsStat([]string{path})
	}); err != nil {
		t.Fatalf("stat: %v", err)
	}
	var meta FsMetadata
	if err := json.Unmarshal(out.Bytes(), &meta); err != nil {
		t.Fatalf("stat json: %v", err)
	}
	if meta.Kind != FsKindFile || meta.Size != 11 {
		t.Fatalf("stat unexpected: %+v", meta)
	}

	out.Reset()
	if err := withStdio(nil, &out, func() error {
		return fsList([]string{dir})
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
	var entries []FsEntry
	if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
		t.Fatalf("list json: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != FsKindFile {
		t.Fatalf("list unexpected: %+v", entries)
	}
}

func TestFsMkdirRemoveExistsCopyRename(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := fsMkdir([]string{sub}); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var out bytes.Buffer
	if err := withStdio(nil, &out, func() error {
		return fsExists([]string{sub})
	}); err != nil {
		t.Fatalf("exists: %v", err)
	}
	if strings.TrimSpace(out.String()) != "true" {
		t.Fatalf("exists got %q", out.String())
	}

	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsCopy([]string{src, dst}); err != nil {
		t.Fatalf("copy: %v", err)
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "x" {
		t.Fatalf("copy content: %q %v", b, err)
	}

	renamed := filepath.Join(dir, "c.txt")
	if err := fsRename([]string{dst, renamed}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := fsRemove([]string{renamed}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := fsRemoveDir([]string{sub}); err != nil {
		t.Fatalf("remove-dir: %v", err)
	}

	out.Reset()
	if err := withStdio(nil, &out, func() error {
		return fsExists([]string{sub})
	}); err != nil {
		t.Fatalf("exists after: %v", err)
	}
	if strings.TrimSpace(out.String()) != "false" {
		t.Fatalf("exists after got %q", out.String())
	}
}

func TestFsRemoveRejectsDir(t *testing.T) {
	dir := t.TempDir()
	if err := fsRemove([]string{dir}); err == nil {
		t.Fatal("expected remove to reject directory")
	}
}

func TestFsRemoveDirRejectsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsRemoveDir([]string{path}); err == nil {
		t.Fatal("expected remove-dir to reject file")
	}
}

func TestFsCopyRejectsDir(t *testing.T) {
	dir := t.TempDir()
	if err := fsCopy([]string{dir, filepath.Join(dir, "x")}); err == nil {
		t.Fatal("expected copy to reject directory")
	}
}

func TestRunFsCLI(t *testing.T) {
	handled, err := RunFsCLI([]string{"job", "status"})
	if handled || err != nil {
		t.Fatalf("expected not handled, got %v %v", handled, err)
	}
	handled, err = RunFsCLI([]string{"fs"})
	if !handled || err == nil {
		t.Fatalf("expected usage error, got %v %v", handled, err)
	}
	handled, err = RunFsCLI([]string{"fs", "nope"})
	if !handled || err == nil {
		t.Fatalf("expected unknown subcommand, got %v %v", handled, err)
	}
}

func withStdio(in io.Reader, out io.Writer, fn func() error) error {
	oldIn, oldOut := os.Stdin, os.Stdout
	defer func() {
		os.Stdin = oldIn
		os.Stdout = oldOut
	}()
	if in != nil {
		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		os.Stdin = r
		go func() {
			_, _ = io.Copy(w, in)
			_ = w.Close()
		}()
		defer r.Close()
	}
	if out != nil {
		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		os.Stdout = w
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(out, r)
			close(done)
		}()
		err = fn()
		_ = w.Close()
		<-done
		_ = r.Close()
		return err
	}
	return fn()
}
