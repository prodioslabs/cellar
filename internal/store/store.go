package store

import (
	"context"
	"errors"
	"time"

	"github.com/prodioslabs/cellar/internal/apikey"
	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/token"
)

var (
	// ErrNotInitialized indicates the cluster has not been bootstrapped.
	ErrNotInitialized = errors.New("cluster not initialized")
	// ErrAlreadyInitialized indicates CreateCluster was called twice.
	ErrAlreadyInitialized = errors.New("cluster already initialized")
	// ErrNodeNotFound indicates a node record is missing.
	ErrNodeNotFound = errors.New("node not found")
	// ErrSandboxNotFound indicates a sandbox record is missing.
	ErrSandboxNotFound = errors.New("sandbox not found")
	// ErrAPIKeyNotFound indicates an API key record is missing.
	ErrAPIKeyNotFound = errors.New("api key not found")
	// ErrNotLeader indicates the node is not the Raft leader.
	ErrNotLeader = errors.New("not the raft leader")
)

// RootCAMaterial is the raft-replicated cluster CA (SwarmKit-style Cluster.RootCA).
type RootCAMaterial struct {
	CAKey       []byte        `json:"ca_key"`
	CACert      []byte        `json:"ca_cert"`
	CACertHash  string        `json:"ca_cert_hash"`
	JoinSecrets token.Secrets `json:"join_secrets"`
}

// Cluster is the singleton cluster object stored in Raft.
type Cluster struct {
	ClusterID    string         `json:"cluster_id"`
	RootCA       RootCAMaterial `json:"root_ca"`
	CertValidity time.Duration  `json:"cert_validity_ns"`
	CreatedAt    time.Time      `json:"created_at"`
}

// Redact returns a deep copy of c with CAKey cleared for external APIs.
func (c *Cluster) Redact() *Cluster {
	if c == nil {
		return nil
	}
	out := *c
	out.RootCA.CAKey = nil
	out.RootCA.JoinSecrets = token.Secrets{}
	return &out
}

// LoadRootCA builds a signing RootCA from cluster material.
func (c *Cluster) LoadRootCA() (*ca.RootCA, error) {
	if c == nil {
		return nil, ErrNotInitialized
	}
	return ca.LoadRootCA(c.RootCA.CACert, c.RootCA.CAKey)
}

// ClusterConfig is the input to CreateCluster / InitCluster.
type ClusterConfig struct {
	ClusterID    string
	CertValidity time.Duration
	RootCA       *ca.RootCA
	JoinSecrets  token.Secrets
}

// Store abstracts persistence for the cluster CA and node records.
type Store interface {
	CreateCluster(ctx context.Context, cfg ClusterConfig) error
	IsInitialized(ctx context.Context) (bool, error)
	GetCluster(ctx context.Context) (*Cluster, error)
	UpdateCluster(ctx context.Context, cluster *Cluster) error
	GetRootCA(ctx context.Context) (*ca.RootCA, error)
	GetNode(ctx context.Context, nodeID string) (*node.Node, error)
	SaveNode(ctx context.Context, n *node.Node) error
	DeleteNode(ctx context.Context, nodeID string) error
	ListNodes(ctx context.Context) ([]*node.Node, error)

	SaveSandbox(ctx context.Context, sb *sandbox.Sandbox) error
	DeleteSandbox(ctx context.Context, id string) error
	GetSandbox(ctx context.Context, id string) (*sandbox.Sandbox, error)
	ListSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error)
	ListSandboxesByNode(ctx context.Context, nodeID string) ([]*sandbox.Sandbox, error)

	SaveAPIKey(ctx context.Context, key *apikey.Key) error
	DeleteAPIKey(ctx context.Context, id string) error
	GetAPIKey(ctx context.Context, id string) (*apikey.Key, error)
	GetAPIKeyByHash(ctx context.Context, keyHash string) (*apikey.Key, error)
	ListAPIKeys(ctx context.Context) ([]*apikey.Key, error)
}
