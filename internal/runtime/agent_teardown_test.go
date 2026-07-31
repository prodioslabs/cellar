package runtime

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeStopRemove struct {
	mu      sync.Mutex
	stopped []string
	removed []string
}

func (f *fakeStopRemove) Stop(_ context.Context, containerID string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, containerID)
	return nil
}

func (f *fakeStopRemove) Remove(_ context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, containerID)
	return nil
}

func TestTeardownLocal(t *testing.T) {
	dataDir := t.TempDir()

	fake := &fakeStopRemove{}
	a := NewAgent("node-1", nil, nil, nil, nil, nil, dataDir, "")
	a.stopRemove = fake
	a.local = map[string]string{
		"sb-a": "cid-a",
		"sb-b": "cid-b",
	}

	for _, id := range []string{"sb-a", "sb-b"} {
		if _, err := PrepareSandboxDir(dataDir, id); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.TeardownLocal(ctx)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.stopped) != 2 || len(fake.removed) != 2 {
		t.Fatalf("stopped=%v removed=%v", fake.stopped, fake.removed)
	}
	stopped := map[string]bool{}
	for _, id := range fake.stopped {
		stopped[id] = true
	}
	removed := map[string]bool{}
	for _, id := range fake.removed {
		removed[id] = true
	}
	for _, cid := range []string{"cid-a", "cid-b"} {
		if !stopped[cid] || !removed[cid] {
			t.Fatalf("missing stop/remove for %s: stopped=%v removed=%v", cid, fake.stopped, fake.removed)
		}
	}

	if len(a.local) != 0 {
		t.Fatalf("local not emptied: %v", a.local)
	}
	for _, id := range []string{"sb-a", "sb-b"} {
		if _, err := os.Stat(SandboxHostDir(dataDir, id)); !os.IsNotExist(err) {
			t.Fatalf("sandbox dir %s still present: %v", id, err)
		}
	}

	if entries, err := os.ReadDir(filepath.Join(dataDir, "sandboxes")); err == nil && len(entries) != 0 {
		t.Fatalf("sandboxes dir not empty: %v", entries)
	}
}
