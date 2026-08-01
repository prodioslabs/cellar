package egress_test

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/prodioslabs/cellar/internal/egress"
)

func TestAllocateFreePersist(t *testing.T) {
	dir := t.TempDir()
	a, err := egress.NewAllocator(dir, "172.30.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	n1, err := a.Allocate("sb-a")
	if err != nil {
		t.Fatal(err)
	}
	if n1.String() == "" {
		t.Fatal("empty subnet")
	}
	n2, err := a.Allocate("sb-a")
	if err != nil {
		t.Fatal(err)
	}
	if n1.String() != n2.String() {
		t.Fatalf("idempotent allocate: %s vs %s", n1, n2)
	}
	n3, err := a.Allocate("sb-b")
	if err != nil {
		t.Fatal(err)
	}
	if n1.String() == n3.String() {
		t.Fatal("duplicate subnet")
	}

	gw := egress.GatewayIP(n1)
	sb := egress.SandboxIP(n1)
	if gw.String() != offsetExpect(n1, 2) {
		t.Fatalf("gateway ip %s", gw)
	}
	if sb.String() != offsetExpect(n1, 3) {
		t.Fatalf("sandbox ip %s", sb)
	}

	if err := a.Free("sb-a"); err != nil {
		t.Fatal(err)
	}
	// Reload
	a2, err := egress.NewAllocator(dir, "172.30.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a2.Lookup("sb-a"); ok {
		t.Fatal("sb-a should be freed")
	}
	if got, ok := a2.Lookup("sb-b"); !ok || got.String() != n3.String() {
		t.Fatalf("sb-b lookup: %v %v", got, ok)
	}
	if _, err := filepath.Rel(dir, filepath.Join(dir, "egress", "ipam.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSyncFromDocker(t *testing.T) {
	dir := t.TempDir()
	a, err := egress.NewAllocator(dir, "172.30.0.0/28")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.Allocate("old")
	if err := a.SyncFromDocker(map[string]string{
		"live": "172.30.0.8/29",
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Lookup("old"); ok {
		t.Fatal("old should be gone")
	}
	got, ok := a.Lookup("live")
	if !ok || got.String() != "172.30.0.8/29" {
		t.Fatalf("live: %v %v", got, ok)
	}
}

func offsetExpect(n *net.IPNet, off int) string {
	ip := make(net.IP, 4)
	copy(ip, n.IP.To4())
	ip[3] += byte(off)
	return ip.String()
}
