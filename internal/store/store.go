package store

import (
	"context"
	"errors"
	"time"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/token"
)

var (
	// ErrNotInitialized indicates the cluster has not been bootstrapped.
	ErrNotInitialized = errors.New("cluster not initialized")
	// ErrAlreadyInitialized indicates InitCluster was called twice.
	ErrAlreadyInitialized = errors.New("cluster already initialized")
	// ErrNodeNotFound indicates a node record is missing.
	ErrNodeNotFound = errors.New("node not found")
)

// ClusterState is persisted cluster configuration (without private keys).
type ClusterState struct {
	ClusterID     string        `json:"cluster_id"`
	CertValidity  time.Duration `json:"cert_validity_ns"`
	JoinSecrets   token.Secrets `json:"join_secrets"`
	CreatedAt     time.Time     `json:"created_at"`
	CADigestPrefix string       `json:"ca_digest_prefix"`
}

// ClusterConfig is the input to InitCluster.
type ClusterConfig struct {
	ClusterID    string
	CertValidity time.Duration
	RootCA       *ca.RootCA
	JoinSecrets  token.Secrets
}

// Store abstracts persistence so FileStore can later be swapped for RaftStore.
type Store interface {
	InitCluster(ctx context.Context, cfg ClusterConfig) error
	IsInitialized(ctx context.Context) (bool, error)
	GetRootCA(ctx context.Context) (*ca.RootCA, error)
	SaveRootCA(ctx context.Context, root *ca.RootCA) error
	GetCluster(ctx context.Context) (*ClusterState, error)
	SaveCluster(ctx context.Context, state *ClusterState) error
	GetNode(ctx context.Context, nodeID string) (*node.Node, error)
	SaveNode(ctx context.Context, n *node.Node) error
	ListNodes(ctx context.Context) ([]*node.Node, error)
}
