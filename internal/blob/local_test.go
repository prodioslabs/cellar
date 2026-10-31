package blob

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalStorePutGetDelete(t *testing.T) {
	root := t.TempDir()
	s, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend() != "local" {
		t.Fatalf("backend=%s", s.Backend())
	}
	key := "snapshots/demo/snapshot_abc.tar"
	if err := s.Put(context.Background(), key, bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "snapshots", "demo", "snapshot_abc.tar")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || string(b) != "hello" {
		t.Fatalf("got %q err=%v", b, err)
	}
	if err := s.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), key); err == nil {
		t.Fatal("expected missing")
	}
}

func TestLocalStoreRejectsEscape(t *testing.T) {
	s, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), "../etc/passwd", bytes.NewReader([]byte("x"))); err == nil {
		t.Fatal("expected reject")
	}
}

func TestOpenLocalDefault(t *testing.T) {
	store, err := Open(context.Background(), Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if store.Backend() != "local" {
		t.Fatalf("backend=%s", store.Backend())
	}
}
