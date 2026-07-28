package raftstore

import (
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/store"
)

func TestNodeSaveDelete(t *testing.T) {
	fsm := NewFSM()
	n := &node.Node{
		ID:           "n1",
		Role:         node.RoleWorker,
		Membership:   node.MembershipAccepted,
		Availability: node.AvailabilityDrain,
		Labels:       map[string]string{"zone": "a"},
		IssuedAt:     time.Now().UTC(),
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
	}
	data, err := encodeCommand(opSaveNode, saveNodePayload{Node: n})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: data}); resp != nil {
		t.Fatal(resp)
	}
	got, err := fsm.getNode("n1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Availability != node.AvailabilityDrain || got.Labels["zone"] != "a" {
		t.Fatalf("got=%+v", got)
	}
	del, err := encodeCommand(opDeleteNode, deleteNodePayload{ID: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: del}); resp != nil {
		t.Fatal(resp)
	}
	if _, err := fsm.getNode("n1"); !errors.Is(err, store.ErrNodeNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestPeerSaveDelete(t *testing.T) {
	fsm := NewFSM()
	data, err := encodeCommand(opSavePeer, savePeerPayload{Peer: PeerInfo{
		NodeID: "m1", RaftAddr: "10.0.0.1:17947", GRPCAddr: "10.0.0.1:17946",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: data}); resp != nil {
		t.Fatal(resp)
	}
	if _, ok := fsm.getPeer("m1"); !ok {
		t.Fatal("peer missing")
	}
	del, err := encodeCommand(opDeletePeer, deletePeerPayload{ID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: del}); resp != nil {
		t.Fatal(resp)
	}
	if _, ok := fsm.getPeer("m1"); ok {
		t.Fatal("peer still present")
	}
}
