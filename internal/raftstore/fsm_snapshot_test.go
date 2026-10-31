package raftstore

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/store"
)

func TestFSSnapshotSaveDeleteAndRestore(t *testing.T) {
	fsm := NewFSM()
	now := time.Now().UTC().Truncate(time.Second)
	snap := &sandbox.FSSnapshot{
		ID:           "snap1",
		SandboxID:    "sb1",
		SandboxName:  "demo",
		Kind:         sandbox.FSSnapshotKindAutomatic,
		Status:       sandbox.FSSnapshotStatusCreated,
		Backend:      sandbox.FSSnapshotBackendLocal,
		Location:     sandbox.SnapshotObjectKey("demo", "snap1"),
		ExpirationMs: sandbox.DefaultSnapshotExpiration.Milliseconds(),
		LastUsedAt:   now,
		ExpiresAt:    now.Add(sandbox.DefaultSnapshotExpiration),
		CreatedAt:    now,
		UpdatedAt:    now,
		SpecJSON:     []byte(`{"name":"demo"}`),
	}
	data, err := encodeCommand(opSaveFSSnapshot, saveFSSnapshotPayload{Snapshot: snap})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: data}); resp != nil {
		t.Fatal(resp)
	}
	got, err := fsm.getFSSnapshot("snap1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Location != "snapshots/demo/snapshot_snap1.tar" {
		t.Fatalf("location=%q", got.Location)
	}
	bySB := fsm.listFSSnapshotsBySandbox("sb1")
	if len(bySB) != 1 {
		t.Fatalf("by sandbox=%d", len(bySB))
	}

	sink := &memSink{buf: &bytes.Buffer{}}
	fs, err := fsm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.Persist(sink); err != nil {
		t.Fatal(err)
	}
	restored := NewFSM()
	if err := restored.Restore(io.NopCloser(bytes.NewReader(sink.buf.Bytes()))); err != nil {
		t.Fatal(err)
	}
	got, err = restored.getFSSnapshot("snap1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.SpecJSON) != `{"name":"demo"}` {
		t.Fatalf("spec=%s", got.SpecJSON)
	}

	del, err := encodeCommand(opDeleteFSSnapshot, deleteFSSnapshotPayload{ID: "snap1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: del}); resp != nil {
		t.Fatal(resp)
	}
	if _, err := fsm.getFSSnapshot("snap1"); !errors.Is(err, store.ErrFSSnapshotNotFound) {
		t.Fatalf("err=%v", err)
	}
}
