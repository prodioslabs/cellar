package egress

import (
	"net"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestAllowAnyUsesResolvedHostname(t *testing.T) {
	p := NewProxy()
	p.SetPolicy("sb1", sandbox.NetworkPolicy{
		Mode: sandbox.NetworkAllowlist,
		Rules: []sandbox.NetworkRule{
			{Hosts: []string{"example.com"}, Ports: []uint32{443}, Protocols: []string{"tcp"}},
		},
		DNS: sandbox.DNSPolicy{
			Mode:  sandbox.DNSAllowlist,
			Names: []string{"example.com"},
		},
	})

	const resolved = "93.184.216.34"
	if p.allowAny(resolved, 443) {
		t.Fatal("expected deny before DNS cache")
	}

	p.rememberResolved("example.com", net.ParseIP(resolved))
	if !p.allowAny(resolved, 443) {
		t.Fatal("expected allow via cached hostname")
	}
	if p.allowAny(resolved, 80) {
		t.Fatal("expected deny wrong port")
	}
	if p.allowAny("198.51.100.1", 443) {
		t.Fatal("expected deny unknown IP")
	}
}

func TestResolvedNameExpiry(t *testing.T) {
	p := NewProxy()
	p.SetPolicy("sb1", sandbox.NetworkPolicy{
		Mode: sandbox.NetworkAllowlist,
		Rules: []sandbox.NetworkRule{
			{Hosts: []string{"example.com"}, Ports: []uint32{443}},
		},
	})

	const resolved = "93.184.216.34"
	p.mu.Lock()
	p.resolvedIP[resolved] = resolvedIPEntry{
		name:    "example.com",
		expires: time.Now().Add(-time.Second),
	}
	p.mu.Unlock()

	if name, ok := p.resolvedName(resolved); ok || name != "" {
		t.Fatalf("expected expired entry miss, got %q ok=%v", name, ok)
	}
	if p.allowAny(resolved, 443) {
		t.Fatal("expected deny for expired DNS cache entry")
	}
}

func TestRememberResolvedAndResolvedName(t *testing.T) {
	p := NewProxy()
	ip := net.ParseIP("203.0.113.10")
	p.rememberResolved("api.example.com", ip)

	name, ok := p.resolvedName("203.0.113.10")
	if !ok || name != "api.example.com" {
		t.Fatalf("resolvedName: got %q ok=%v", name, ok)
	}
	if _, ok := p.resolvedName("203.0.113.11"); ok {
		t.Fatal("expected miss for other IP")
	}
}
