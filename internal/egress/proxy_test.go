package egress

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

func allowlistPolicy(hosts ...string) sandbox.NetworkPolicy {
	return sandbox.NetworkPolicy{
		Mode: sandbox.NetworkAllowlist,
		Rules: []sandbox.NetworkRule{
			{Hosts: hosts, Ports: []uint32{443}, Protocols: []string{"tcp"}},
		},
		DNS: sandbox.DNSPolicy{Mode: sandbox.DNSAllowlist, Names: hosts},
	}
}

// Co-located sandboxes must not share an allowlist: each connection is judged
// only by the policy of the sandbox that opened it.
func TestEvaluatorForIsolatesSandboxes(t *testing.T) {
	p := NewProxy()
	p.SetPolicy("sb1", allowlistPolicy("one.example.com"))
	p.BindSandboxIP("sb1", "172.30.0.2")
	p.SetPolicy("sb2", allowlistPolicy("two.example.com"))
	p.BindSandboxIP("sb2", "172.30.0.3")

	ev, id, ok := p.evaluatorFor("172.30.0.2:51000")
	if !ok || id != "sb1" {
		t.Fatalf("evaluatorFor(sb1 ip) = %q ok=%v", id, ok)
	}
	if d, _ := ev.AllowConnect("one.example.com", net.ParseIP("93.184.216.34"), 443); d != Allow {
		t.Fatal("sb1 should reach its own allowlisted host")
	}
	if d, _ := ev.AllowConnect("two.example.com", net.ParseIP("93.184.216.34"), 443); d != Deny {
		t.Fatal("sb1 must not inherit sb2's allowlist")
	}

	ev2, id, ok := p.evaluatorFor("172.30.0.3:51000")
	if !ok || id != "sb2" {
		t.Fatalf("evaluatorFor(sb2 ip) = %q ok=%v", id, ok)
	}
	if d, _ := ev2.AllowConnect("one.example.com", net.ParseIP("93.184.216.34"), 443); d != Deny {
		t.Fatal("sb2 must not inherit sb1's allowlist")
	}
}

func TestEvaluatorForUnknownSourceFailsClosed(t *testing.T) {
	p := NewProxy()
	p.SetPolicy("sb1", allowlistPolicy("example.com"))
	p.BindSandboxIP("sb1", "172.30.0.2")

	if _, _, ok := p.evaluatorFor("172.30.0.9:51000"); ok {
		t.Fatal("unbound source IP must not resolve to a sandbox")
	}
	if _, _, ok := p.evaluatorFor("bogus"); ok {
		t.Fatal("unparsable source must not resolve to a sandbox")
	}
	// Bound but with no policy registered is also a miss.
	p.BindSandboxIP("sb-nopolicy", "172.30.0.4")
	if _, _, ok := p.evaluatorFor("172.30.0.4:1234"); ok {
		t.Fatal("sandbox without a policy must fail closed")
	}
}

func TestUnbindAndRebind(t *testing.T) {
	p := NewProxy()
	p.SetPolicy("sb1", allowlistPolicy("example.com"))
	p.BindSandboxIP("sb1", "172.30.0.2")

	// Rebinding to a new IP drops the stale reverse entry.
	p.BindSandboxIP("sb1", "172.30.0.5")
	if _, _, ok := p.evaluatorFor("172.30.0.2:1"); ok {
		t.Fatal("old IP should no longer resolve")
	}
	if _, id, ok := p.evaluatorFor("172.30.0.5:1"); !ok || id != "sb1" {
		t.Fatalf("new IP should resolve to sb1, got %q ok=%v", id, ok)
	}

	// Docker hands the address to the next container; the departed sandbox
	// must not steal it back on unbind.
	p.SetPolicy("sb2", allowlistPolicy("example.com"))
	p.BindSandboxIP("sb2", "172.30.0.5")
	p.UnbindSandbox("sb1")
	if _, id, ok := p.evaluatorFor("172.30.0.5:1"); !ok || id != "sb2" {
		t.Fatalf("reused IP should still resolve to sb2, got %q ok=%v", id, ok)
	}
}

func TestRemovePolicyUnbinds(t *testing.T) {
	p := NewProxy()
	p.SetPolicy("sb1", allowlistPolicy("example.com"))
	p.BindSandboxIP("sb1", "172.30.0.2")

	p.RemovePolicy("sb1")
	if p.HasPolicy("sb1") {
		t.Fatal("policy should be gone")
	}
	if _, ok := p.SandboxIP("sb1"); ok {
		t.Fatal("binding should be gone")
	}
	if _, _, ok := p.evaluatorFor("172.30.0.2:1"); ok {
		t.Fatal("removed sandbox IP must fail closed")
	}
}

func TestDestinationDeniedRespectsExceptions(t *testing.T) {
	p := NewProxy()
	if !p.destinationDenied(net.ParseIP("169.254.169.254")) {
		t.Fatal("metadata address must be denied by default")
	}
	if p.destinationDenied(net.ParseIP("93.184.216.34")) {
		t.Fatal("public address must not be denied")
	}
	if err := p.SetPrivateExceptions([]string{"10.20.0.0/16"}); err != nil {
		t.Fatalf("SetPrivateExceptions: %v", err)
	}
	if p.destinationDenied(net.ParseIP("10.20.1.1")) {
		t.Fatal("exempted address should be allowed through")
	}
	if !p.destinationDenied(net.ParseIP("10.30.1.1")) {
		t.Fatal("address outside the exception must stay denied")
	}
	if err := p.SetPrivateExceptions([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected an error for an invalid CIDR")
	}
}

func TestSetPolicyClosesNewlyDeniedConns(t *testing.T) {
	p := NewProxy()
	p.SetPolicy("sb1", allowlistPolicy("keep.example.com", "drop.example.net"))

	kept, keptPeer := net.Pipe()
	defer keptPeer.Close()
	revoked, revokedPeer := net.Pipe()
	defer revokedPeer.Close()

	p.trackConn("sb1", &liveConn{conn: kept, hostname: "keep.example.com", ip: net.ParseIP("93.184.216.34"), port: 443})
	p.trackConn("sb1", &liveConn{conn: revoked, hostname: "drop.example.net", ip: net.ParseIP("198.51.100.7"), port: 443})

	// The reconcile loop re-applies desired state every tick, so an unchanged
	// policy must not disturb established connections.
	p.SetPolicy("sb1", allowlistPolicy("keep.example.com", "drop.example.net"))
	if closed(t, kept) || closed(t, revoked) {
		t.Fatal("re-applying the same policy must not close connections")
	}

	p.SetPolicy("sb1", allowlistPolicy("keep.example.com"))
	if !closed(t, revoked) {
		t.Fatal("expected the newly denied connection to be closed")
	}
	if closed(t, kept) {
		t.Fatal("expected the still-allowed connection to survive")
	}
}

func TestRemovePolicyClosesConns(t *testing.T) {
	p := NewProxy()
	p.SetPolicy("sb1", allowlistPolicy("example.com"))
	c, peer := net.Pipe()
	defer peer.Close()
	p.trackConn("sb1", &liveConn{conn: c, hostname: "example.com", ip: net.ParseIP("93.184.216.34"), port: 443})

	p.RemovePolicy("sb1")
	if !closed(t, c) {
		t.Fatal("expected connection to be closed with the policy")
	}
}

// closed reports whether a net.Pipe endpoint has been closed.
func closed(t *testing.T, c net.Conn) bool {
	t.Helper()
	_ = c.SetWriteDeadline(time.Now().Add(10 * time.Millisecond))
	_, err := c.Write([]byte{0})
	return err == io.ErrClosedPipe || errors.Is(err, net.ErrClosed)
}
