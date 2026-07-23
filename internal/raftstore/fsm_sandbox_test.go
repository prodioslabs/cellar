package raftstore

import (
	"errors"
	"testing"

	"github.com/hashicorp/raft"

	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/store"
)

func TestSandboxSaveDelete(t *testing.T) {
	fsm := NewFSM()
	sb := &sandbox.Sandbox{
		ID:           "sb1",
		DesiredState: sandbox.DesiredRunning,
		Spec:         sandbox.Spec{Image: "alpine"},
		Status:       sandbox.Status{Phase: sandbox.PhasePending},
	}
	data, err := encodeCommand(opSaveSandbox, saveSandboxPayload{Sandbox: sb})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: data}); resp != nil {
		t.Fatal(resp)
	}
	got, err := fsm.getSandbox("sb1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.Image != "alpine" {
		t.Fatalf("image=%q", got.Spec.Image)
	}
	del, err := encodeCommand(opDeleteSandbox, deleteSandboxPayload{ID: "sb1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: del}); resp != nil {
		t.Fatal(resp)
	}
	if _, err := fsm.getSandbox("sb1"); !errors.Is(err, store.ErrSandboxNotFound) {
		t.Fatalf("err=%v", err)
	}
}
