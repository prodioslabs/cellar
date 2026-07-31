package grpcapi

import (
	"crypto/x509"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/prodioslabs/cellar/internal/node"
)

// IdentityProvider supplies mTLS material for forwarding RPCs to the Raft leader.
type IdentityProvider interface {
	IdentityPEMs() (cert, key, ca []byte, err error)
}

// managerEndpoints returns the current leader gRPC address and the set of known
// manager gRPC addresses from Raft peer metadata.
func managerEndpoints(raft RaftAdmin) (leader string, addrs []string) {
	if raft == nil {
		return "", nil
	}
	leader = raft.LeaderGRPC()
	seen := make(map[string]struct{})
	add := func(a string) {
		if a == "" {
			return
		}
		if _, ok := seen[a]; ok {
			return
		}
		seen[a] = struct{}{}
		addrs = append(addrs, a)
	}
	if leader != "" {
		add(leader)
	}
	for _, p := range raft.ListPeers() {
		add(p.GRPCAddr)
	}
	add(raft.GRPCAdvertise())
	return leader, addrs
}

// MergeManagerAddrs returns a deduplicated list preserving first-seen order,
// always including prefer when non-empty.
func MergeManagerAddrs(prefer string, lists ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(a string) {
		if a == "" {
			return
		}
		if _, ok := seen[a]; ok {
			return
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	add(prefer)
	for _, list := range lists {
		for _, a := range list {
			add(a)
		}
	}
	return out
}

// requireNodeOrManagerPeer accepts a peer whose CN matches nodeID, or any
// manager-role certificate (trusted forwarder for leader-proxied RPCs).
func requireNodeOrManagerPeer(ctxPeer *x509.Certificate, nodeID string) error {
	if ctxPeer == nil {
		return status.Error(codes.Unauthenticated, "client certificate required")
	}
	if nodeID != "" && ctxPeer.Subject.CommonName == nodeID {
		return nil
	}
	role, err := node.RoleFromCertificate(ctxPeer)
	if err != nil {
		return status.Error(codes.Unauthenticated, "invalid client certificate")
	}
	if !role.CanAccessControlPlane() {
		return status.Error(codes.PermissionDenied, "node_id does not match certificate")
	}
	return nil
}
