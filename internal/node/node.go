package node

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Role identifies a node's type in the cluster.
// Cellar uses a single node type for all peers.
type Role string

const (
	// RoleCellarNode is the only node type.
	RoleCellarNode Role = "cellar-node"
)

// OU is the Organizational Unit embedded in node certificates.
const OU = "cellar-node"

// ParseOU maps a certificate OU back to a Role.
func ParseOU(ou string) (Role, error) {
	if ou == OU {
		return RoleCellarNode, nil
	}
	return "", fmt.Errorf("unknown OU %q", ou)
}

// Membership reflects whether a node is accepted into the cluster.
type Membership string

const (
	MembershipAccepted Membership = "accepted"
	MembershipPending  Membership = "pending"
)

// Node is a registered cluster node record.
type Node struct {
	ID                string     `json:"node_id"`
	Role              Role       `json:"role"`
	Membership        Membership `json:"membership"`
	PubKeyFingerprint string     `json:"csr_pubkey_fingerprint"`
	IssuedAt          time.Time  `json:"issued_at"`
	ExpiresAt         time.Time  `json:"expires_at"`
	CertificatePEM    string     `json:"certificate,omitempty"`
}

// NewID generates a 32-byte hex node ID (64 hex characters).
func NewID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
