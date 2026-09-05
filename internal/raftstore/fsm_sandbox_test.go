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
		Name:         "sb1",
		DesiredState: sandbox.DesiredRunning,
		Spec: sandbox.Spec{
			Name:      "sb1",
			Image:     sandbox.OCIImage("alpine"),
			Resources: sandbox.Resources{VCPUs: 1, MemoryMiB: 512},
		},
		Status: sandbox.Status{Phase: sandbox.PhasePending},
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
	if got.Spec.ImageReference() != "alpine" {
		t.Fatalf("image=%q", got.Spec.ImageReference())
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
