package gateway

import (
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/grpcapi"
	"github.com/prodioslabs/cellar/internal/identity"
	"github.com/prodioslabs/cellar/internal/node"
)

func TestDataDirResolverManagerAddrs(t *testing.T) {
	dir := t.TempDir()
	root, err := ca.GenerateRootCA("cellar-test", ca.DefaultCAValidity)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, certPEM, keyPEM, nb, na, err := grpcapi.SelfIssue(root, node.RoleWorker, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := identity.NewStore(dir)
	mat := &identity.Material{
		NodeID:      nodeID,
		Role:        node.RoleWorker,
		Certificate: certPEM,
		PrivateKey:  keyPEM,
		CACert:      root.CertPEM,
		NotBefore:   nb,
		NotAfter:    na,
	}
	state := identity.DaemonState{
		NodeID:       nodeID,
		Role:         node.RoleWorker,
		ManagerAddr:  "old:17946",
		ManagerAddrs: []string{"old:17946", "new:17946", "peer:17946"},
		Initialized:  true,
	}
	if err := store.Save(mat, state); err != nil {
		t.Fatal(err)
	}

	r := &DataDirResolver{DataDir: dir}
	addrs, caPEM, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(caPEM) == 0 {
		t.Fatal("empty ca")
	}
	if len(addrs) < 2 {
		t.Fatalf("addrs=%v", addrs)
	}
	if addrs[0] != "old:17946" {
		t.Fatalf("prefer first=%v", addrs)
	}
	seen := map[string]bool{}
	for _, a := range addrs {
		seen[a] = true
	}
	if !seen["new:17946"] || !seen["peer:17946"] {
		t.Fatalf("missing rediscovered addrs: %v", addrs)
	}

	r.Overrides = []string{"override:1"}
	addrs, _, err = r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || addrs[0] != "override:1" {
		t.Fatalf("overrides=%v", addrs)
	}
}
