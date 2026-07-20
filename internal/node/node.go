package node

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Role identifies a node's role in the cluster.
type Role string

const (
	RoleWorker  Role = "worker"
	RoleManager Role = "manager"
)

// OU returns the Organizational Unit used in node certificates.
func (r Role) OU() string {
	return "cellar-" + string(r)
}

// ParseOU maps a certificate OU back to a Role.
func ParseOU(ou string) (Role, error) {
	switch ou {
	case "cellar-worker":
		return RoleWorker, nil
	case "cellar-manager":
		return RoleManager, nil
	default:
		return "", fmt.Errorf("unknown OU %q", ou)
	}
}

// Membership reflects whether a node is accepted into the cluster.
type Membership string

const (
	MembershipAccepted Membership = "accepted"
	MembershipPending  Membership = "pending"
)

// Node is a registered cluster node record.
type Node struct {
	ID                  string     `json:"node_id"`
	Role                Role       `json:"role"`
	Membership          Membership `json:"membership"`
	PubKeyFingerprint   string     `json:"csr_pubkey_fingerprint"`
	IssuedAt            time.Time  `json:"issued_at"`
	ExpiresAt           time.Time  `json:"expires_at"`
	CertificatePEM      string     `json:"certificate,omitempty"`
}

// NewID generates a 32-byte hex node ID (64 hex characters).
func NewID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
