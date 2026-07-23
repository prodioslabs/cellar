package egress_test

import (
	"testing"

	"github.com/prodioslabs/cellar/internal/egress"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestAllowlist(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode: sandbox.NetworkAllowlist,
		Rules: []sandbox.NetworkRule{
			{Hosts: []string{"api.example.com", "10.0.0.0/8"}, Ports: []uint32{443}},
		},
	})
	if ev.AllowConnect("api.example.com", 443) != egress.Allow {
		t.Fatal("expected allow")
	}
	if ev.AllowConnect("api.example.com", 80) != egress.Deny {
		t.Fatal("expected deny wrong port")
	}
	if ev.AllowConnect("evil.com", 443) != egress.Deny {
		t.Fatal("expected deny host")
	}
	if ev.AllowConnect("10.1.2.3", 443) != egress.Allow {
		t.Fatal("expected allow CIDR")
	}
}

func TestDenylist(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode: sandbox.NetworkDenylist,
		Rules: []sandbox.NetworkRule{
			{Hosts: []string{".bad.com"}},
		},
	})
	if ev.AllowConnect("x.bad.com", 80) != egress.Deny {
		t.Fatal("expected deny suffix")
	}
	if ev.AllowConnect("ok.com", 80) != egress.Allow {
		t.Fatal("expected allow")
	}
}

func TestDNSAllowlist(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode: sandbox.NetworkAllowlist,
		DNS: sandbox.DNSPolicy{
			Mode:  sandbox.DNSAllowlist,
			Names: []string{"example.com"},
		},
	})
	if ev.AllowDNS("example.com") != egress.Allow {
		t.Fatal("expected allow dns")
	}
	if ev.AllowDNS("other.com") != egress.Deny {
		t.Fatal("expected deny dns")
	}
}
