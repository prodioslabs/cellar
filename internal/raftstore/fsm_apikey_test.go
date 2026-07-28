package raftstore

import (
	"errors"
	"testing"

	"github.com/hashicorp/raft"

	"github.com/prodioslabs/cellar/internal/apikey"
	"github.com/prodioslabs/cellar/internal/store"
)

func TestAPIKeySaveDelete(t *testing.T) {
	fsm := NewFSM()
	g, err := apikey.Generate("ci")
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodeCommand(opSaveAPIKey, saveAPIKeyPayload{Key: g.Key})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: data}); resp != nil {
		t.Fatal(resp)
	}
	got, err := fsm.getAPIKey(g.Key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ci" || got.KeyHash != g.Key.KeyHash {
		t.Fatalf("got=%+v", got)
	}
	byHash, err := fsm.getAPIKeyByHash(apikey.Hash(g.Raw))
	if err != nil {
		t.Fatal(err)
	}
	if byHash.ID != g.Key.ID {
		t.Fatalf("id=%q", byHash.ID)
	}
	del, err := encodeCommand(opDeleteAPIKey, deleteAPIKeyPayload{ID: g.Key.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: del}); resp != nil {
		t.Fatal(resp)
	}
	if _, err := fsm.getAPIKey(g.Key.ID); !errors.Is(err, store.ErrAPIKeyNotFound) {
		t.Fatalf("err=%v", err)
	}
}
