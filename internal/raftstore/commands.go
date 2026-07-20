package raftstore

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/token"
)

const (
	opInitCluster = "init_cluster"
	opSaveRootCA  = "save_root_ca"
	opSaveCluster = "save_cluster"
	opSaveNode    = "save_node"
	opSavePeer    = "save_peer"
)

// command is the versioned Raft log envelope.
type command struct {
	Op      string          `json:"op"`
	Payload json.RawMessage `json:"payload"`
}

type initClusterPayload struct {
	ClusterID      string        `json:"cluster_id"`
	CertValidity   time.Duration `json:"cert_validity_ns"`
	JoinSecrets    token.Secrets `json:"join_secrets"`
	CreatedAt      time.Time     `json:"created_at"`
	CADigestPrefix string        `json:"ca_digest_prefix"`
	CertPEM        []byte        `json:"cert_pem"`
	KeyPEM         []byte        `json:"key_pem"`
}

type saveRootCAPayload struct {
	CertPEM []byte `json:"cert_pem"`
	KeyPEM  []byte `json:"key_pem"`
}

type saveClusterPayload struct {
	ClusterID      string        `json:"cluster_id"`
	CertValidity   time.Duration `json:"cert_validity_ns"`
	JoinSecrets    token.Secrets `json:"join_secrets"`
	CreatedAt      time.Time     `json:"created_at"`
	CADigestPrefix string        `json:"ca_digest_prefix"`
}

type saveNodePayload struct {
	Node *node.Node `json:"node"`
}

// PeerInfo tracks a manager's Raft and HTTP addresses for leader redirects.
type PeerInfo struct {
	NodeID   string `json:"node_id"`
	RaftAddr string `json:"raft_addr"`
	HTTPAddr string `json:"http_addr"`
}

type savePeerPayload struct {
	Peer PeerInfo `json:"peer"`
}

func encodeCommand(op string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(command{Op: op, Payload: raw})
}

func decodeCommand(data []byte) (command, error) {
	var cmd command
	if err := json.Unmarshal(data, &cmd); err != nil {
		return command{}, fmt.Errorf("decode raft command: %w", err)
	}
	if cmd.Op == "" {
		return command{}, fmt.Errorf("raft command missing op")
	}
	return cmd, nil
}
