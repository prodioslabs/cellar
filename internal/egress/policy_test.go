package egress_test

import (
	"net"
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
	tests := []struct {
		name     string
		hostname string
		ip       string
		port     uint32
		want     egress.Decision
		match    egress.MatchType
	}{
		{"sni match", "api.example.com", "93.184.216.34", 443, egress.Allow, egress.MatchDomain},
		{"sni wrong port", "api.example.com", "93.184.216.34", 80, egress.Deny, egress.MatchNone},
		{"sni not listed", "evil.com", "93.184.216.34", 443, egress.Deny, egress.MatchNone},
		{"cidr match", "", "10.1.2.3", 443, egress.Allow, egress.MatchCIDR},
		{"cidr outside", "", "192.0.2.7", 443, egress.Deny, egress.MatchNone},
		// A hostname is only observable on 80/443, so a name rule cannot
		// authorize any other port even when the IP is right.
		{"no hostname available", "", "93.184.216.34", 443, egress.Deny, egress.MatchNone},
		// The name wins over the IP so the connection is re-dialed by name.
		{"sni preferred over cidr", "api.example.com", "10.1.2.3", 443, egress.Allow, egress.MatchDomain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, match := ev.AllowConnect(tt.hostname, net.ParseIP(tt.ip), tt.port)
			if got != tt.want || match != tt.match {
				t.Fatalf("AllowConnect(%q, %s, %d) = %v/%s, want %v/%s",
					tt.hostname, tt.ip, tt.port, got, match, tt.want, tt.match)
			}
		})
	}
}

func TestDenylist(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode: sandbox.NetworkDenylist,
		Rules: []sandbox.NetworkRule{
			{Hosts: []string{".bad.com", "198.51.100.0/24"}},
		},
	})
	if d, _ := ev.AllowConnect("x.bad.com", net.ParseIP("93.184.216.34"), 80); d != egress.Deny {
		t.Fatal("expected deny by hostname suffix")
	}
	if d, _ := ev.AllowConnect("", net.ParseIP("198.51.100.5"), 22); d != egress.Deny {
		t.Fatal("expected deny by CIDR")
	}
	// Unlisted traffic keeps its original destination rather than re-resolving.
	d, match := ev.AllowConnect("ok.com", net.ParseIP("93.184.216.34"), 80)
	if d != egress.Allow || match != egress.MatchNone {
		t.Fatalf("got %v/%s, want allow/none", d, match)
	}
}

func TestNoneModeDeniesEverything(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{Mode: sandbox.NetworkNone})
	if d, _ := ev.AllowConnect("example.com", net.ParseIP("93.184.216.34"), 443); d != egress.Deny {
		t.Fatal("expected deny in none mode")
	}
	empty := egress.NewEvaluator(sandbox.NetworkPolicy{})
	if d, _ := empty.AllowConnect("example.com", net.ParseIP("93.184.216.34"), 443); d != egress.Deny {
		t.Fatal("expected deny for empty mode")
	}
}

func TestHostPatterns(t *testing.T) {
	tests := []struct {
		pattern string
		host    string
		want    egress.Decision
	}{
		{"*", "anything.example.com", egress.Allow},
		{"*", "example.com", egress.Allow},
		{"*.example.com", "api.example.com", egress.Allow},
		{"*.example.com", "deep.api.example.com", egress.Allow},
		{"*.example.com", "example.com", egress.Allow},
		{"*.example.com", "notexample.com", egress.Deny},
		{"*.example.com", "example.com.evil.net", egress.Deny},
		{".example.com", "api.example.com", egress.Allow},
		{".example.com", "example.com", egress.Allow},
		{"example.com", "example.com", egress.Allow},
		{"example.com", "api.example.com", egress.Allow},
		{"example.com", "notexample.com", egress.Deny},
		{"example.com", "example.com.evil.net", egress.Deny},
		{"API.Example.COM", "api.example.com", egress.Allow},
		{"api.example.com", "API.EXAMPLE.COM.", egress.Allow},
		// Address patterns never match a name.
		{"10.0.0.0/8", "example.com", egress.Deny},
		{"93.184.216.34", "example.com", egress.Deny},
	}
	for _, tt := range tests {
		ev := egress.NewEvaluator(sandbox.NetworkPolicy{
			Mode:  sandbox.NetworkAllowlist,
			Rules: []sandbox.NetworkRule{{Hosts: []string{tt.pattern}}},
		})
		// Pass an IP that no pattern covers so only the name can match.
		got, _ := ev.AllowConnect(tt.host, net.ParseIP("203.0.113.9"), 443)
		if got != tt.want {
			t.Errorf("pattern %q vs host %q = %v, want %v", tt.pattern, tt.host, got, tt.want)
		}
	}
}

func TestAddressPatterns(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode:  sandbox.NetworkAllowlist,
		Rules: []sandbox.NetworkRule{{Hosts: []string{"203.0.113.9", "2001:db8::/32"}}},
	})
	for _, tt := range []struct {
		ip   string
		want egress.Decision
	}{
		{"203.0.113.9", egress.Allow},
		{"203.0.113.10", egress.Deny},
		{"2001:db8::1", egress.Allow},
		{"2001:dead::1", egress.Deny},
	} {
		if got, _ := ev.AllowConnect("", net.ParseIP(tt.ip), 443); got != tt.want {
			t.Errorf("ip %s = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestProtocolScopedRule(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode: sandbox.NetworkAllowlist,
		Rules: []sandbox.NetworkRule{
			{Hosts: []string{"example.com"}, Protocols: []string{"udp"}},
		},
	})
	if d, _ := ev.AllowConnect("example.com", net.ParseIP("93.184.216.34"), 443); d != egress.Deny {
		t.Fatal("expected a udp-only rule not to authorize tcp")
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

func TestDNSFallsBackToRuleHosts(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode: sandbox.NetworkAllowlist,
		Rules: []sandbox.NetworkRule{
			{Hosts: []string{"*.example.com", "10.0.0.0/8"}},
		},
		DNS: sandbox.DNSPolicy{Mode: sandbox.DNSAllowlist},
	})
	if ev.AllowDNS("api.example.com") != egress.Allow {
		t.Fatal("expected allow from rule hosts")
	}
	if ev.AllowDNS("other.com") != egress.Deny {
		t.Fatal("expected deny from rule hosts")
	}
}

func TestBlockAllDeniesEverything(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{Mode: sandbox.NetworkBlockAll})
	if d, _ := ev.AllowConnect("example.com", net.ParseIP("93.184.216.34"), 443); d != egress.Deny {
		t.Fatal("expected deny connect in blockall")
	}
	if ev.AllowDNS("example.com") != egress.Deny {
		t.Fatal("expected deny dns in blockall")
	}
}

func TestEssentialServicesOverride(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode:              sandbox.NetworkBlockAll,
		EssentialServices: true,
	})
	if d, match := ev.AllowConnect("registry.npmjs.org", net.ParseIP("1.2.3.4"), 443); d != egress.Allow || match != egress.MatchDomain {
		t.Fatalf("expected allow essential, got %v/%s", d, match)
	}
	if ev.AllowDNS("proxy.golang.org") != egress.Allow {
		t.Fatal("expected allow essential dns")
	}
	if d, _ := ev.AllowConnect("evil.example.com", net.ParseIP("1.2.3.4"), 443); d != egress.Deny {
		t.Fatal("expected deny non-essential")
	}

	allow := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode:              sandbox.NetworkAllowlist,
		EssentialServices: true,
		Rules:             []sandbox.NetworkRule{{Hosts: []string{"example.com"}}},
		DNS:               sandbox.DNSPolicy{Mode: sandbox.DNSAllowlist, Names: []string{"example.com"}},
	})
	if d, _ := allow.AllowConnect("pypi.org", net.ParseIP("1.2.3.4"), 443); d != egress.Allow {
		t.Fatal("essential should allow beyond rules")
	}
	if d, _ := allow.AllowConnect("evil.com", net.ParseIP("1.2.3.4"), 443); d != egress.Deny {
		t.Fatal("non-essential non-rule should deny")
	}

	none := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode:              sandbox.NetworkNone,
		EssentialServices: true,
	})
	if d, _ := none.AllowConnect("registry.npmjs.org", net.ParseIP("1.2.3.4"), 443); d != egress.Deny {
		t.Fatal("essential must not apply in none mode")
	}
}
