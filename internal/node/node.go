package node

import (
	"crypto/rand"
	"crypto/x509"
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

// CanAccessControlPlane reports whether this role may call control-plane APIs.
// Managers can; workers cannot. Both roles still use the node agent for join,
// renewal, and peer mTLS.
func (r Role) CanAccessControlPlane() bool {
	return r == RoleManager
}

// ParseOU maps a certificate OU back to a Role.
func ParseOU(ou string) (Role, error) {
	switch ou {
	case RoleWorker.OU():
		return RoleWorker, nil
	case RoleManager.OU():
		return RoleManager, nil
	default:
		return "", fmt.Errorf("unknown OU %q", ou)
	}
}

// RoleFromCertificate extracts the cluster role from a leaf certificate's OU.
func RoleFromCertificate(cert *x509.Certificate) (Role, error) {
	if cert == nil || len(cert.Subject.OrganizationalUnit) == 0 {
		return "", fmt.Errorf("certificate has no OU")
	}
	return ParseOU(cert.Subject.OrganizationalUnit[0])
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

	// Runtime heartbeat (managers and workers that run sandboxes).
	RuntimeGRPCAddr    string    `json:"runtime_grpc_addr,omitempty"`
	RuntimeHeartbeatAt time.Time `json:"runtime_heartbeat_at,omitempty"`
	RuntimeSandboxCount int      `json:"runtime_sandbox_count,omitempty"`
}

// NewID generates a 32-byte hex node ID (64 hex characters).
func NewID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
