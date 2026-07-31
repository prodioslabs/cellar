package daemon

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/prodioslabs/cellar/internal/identity"
	"github.com/prodioslabs/cellar/internal/node"
)

func TestSeedManagerAddrs(t *testing.T) {
	prefer, all := seedManagerAddrs("join:17946", "leader:17946", []string{"peer:17946", "join:17946"})
	if prefer != "leader:17946" {
		t.Fatalf("prefer=%q", prefer)
	}
	want := []string{"leader:17946", "join:17946", "peer:17946"}
	if len(all) != len(want) {
		t.Fatalf("all=%v want %v", all, want)
	}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("all=%v want %v", all, want)
		}
	}

	prefer, all = seedManagerAddrs("join:17946", "", nil)
	if prefer != "join:17946" || len(all) != 1 || all[0] != "join:17946" {
		t.Fatalf("prefer=%q all=%v", prefer, all)
	}
}

func TestManagerDialAddrsOrder(t *testing.T) {
	dir := t.TempDir()
	store := identity.NewStore(dir)
	mat := &identity.Material{
		NodeID:      "n1",
		Role:        node.RoleWorker,
		Certificate: []byte("cert"),
		PrivateKey:  []byte("key"),
		CACert:      []byte("ca"),
	}
	state := identity.DaemonState{
		NodeID:       "n1",
		Role:         node.RoleWorker,
		ManagerAddr:  "old:17946",
		ManagerAddrs: []string{"old:17946", "new:17946", "peer:17946"},
		Initialized:  true,
	}
	if err := store.Save(mat, state); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{idStore: store}
	addrs := d.managerDialAddrs()
	if len(addrs) < 2 || addrs[0] != "old:17946" {
		t.Fatalf("addrs=%v", addrs)
	}
	seen := map[string]bool{}
	for _, a := range addrs {
		seen[a] = true
	}
	if !seen["new:17946"] || !seen["peer:17946"] {
		t.Fatalf("missing peers in %v", addrs)
	}
}

func TestApplyManagerEndpoints(t *testing.T) {
	dir := t.TempDir()
	store := identity.NewStore(dir)
	mat := &identity.Material{
		NodeID:      "n1",
		Role:        node.RoleWorker,
		Certificate: []byte("cert"),
		PrivateKey:  []byte("key"),
		CACert:      []byte("ca"),
	}
	state := identity.DaemonState{
		NodeID:      "n1",
		Role:        node.RoleWorker,
		ManagerAddr: "old:17946",
		Initialized: true,
	}
	if err := store.Save(mat, state); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{idStore: store}
	d.applyManagerEndpoints("leader:17946", []string{"leader:17946", "peer:17946"})
	got := store.State()
	if got.ManagerAddr != "leader:17946" {
		t.Fatalf("ManagerAddr=%q", got.ManagerAddr)
	}
	if len(got.ManagerAddrs) < 2 {
		t.Fatalf("ManagerAddrs=%v", got.ManagerAddrs)
	}
	if got.ManagerAddrs[0] != "leader:17946" {
		t.Fatalf("ManagerAddrs=%v", got.ManagerAddrs)
	}
}

func TestForEachManagerFailover(t *testing.T) {
	dir := t.TempDir()
	store := identity.NewStore(dir)
	mat := &identity.Material{
		NodeID:      "n1",
		Role:        node.RoleWorker,
		Certificate: []byte("cert"),
		PrivateKey:  []byte("key"),
		CACert:      []byte("ca"),
	}
	state := identity.DaemonState{
		NodeID:       "n1",
		Role:         node.RoleWorker,
		ManagerAddr:  "dead:17946",
		ManagerAddrs: []string{"dead:17946", "live:17946"},
		Initialized:  true,
	}
	if err := store.Save(mat, state); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{idStore: store}

	tried := []string{}
	err := d.forEachManager(func(addr string) error {
		tried = append(tried, addr)
		if addr == "dead:17946" {
			return status.Error(codes.Unavailable, "connection refused")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tried) < 2 || tried[0] != "dead:17946" || tried[1] != "live:17946" {
		t.Fatalf("tried=%v", tried)
	}
}

func TestForEachManagerNonRetryable(t *testing.T) {
	dir := t.TempDir()
	store := identity.NewStore(dir)
	mat := &identity.Material{
		NodeID:      "n1",
		Role:        node.RoleWorker,
		Certificate: []byte("cert"),
		PrivateKey:  []byte("key"),
		CACert:      []byte("ca"),
	}
	state := identity.DaemonState{
		NodeID:       "n1",
		Role:         node.RoleWorker,
		ManagerAddr:  "a:1",
		ManagerAddrs: []string{"a:1", "b:1"},
		Initialized:  true,
	}
	if err := store.Save(mat, state); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{idStore: store}
	err := d.forEachManager(func(addr string) error {
		return status.Error(codes.PermissionDenied, "nope")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.PermissionDenied {
		t.Fatalf("got %v", err)
	}
}

func TestIsRetryableManagerErr(t *testing.T) {
	if !isRetryableManagerErr(errors.New("dial tcp")) {
		t.Fatal("transport should retry")
	}
	if !isRetryableManagerErr(status.Error(codes.Unavailable, "x")) {
		t.Fatal("unavailable should retry")
	}
	if isRetryableManagerErr(status.Error(codes.InvalidArgument, "x")) {
		t.Fatal("invalid should not retry")
	}
}
