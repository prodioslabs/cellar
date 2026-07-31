package sandbox_test

import (
	"strings"
	"testing"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestResolveNetworkAllowList(t *testing.T) {
	np, err := sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "208.80.154.232/32, 192.168.1.0/24", "", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkAllowlist {
		t.Fatalf("mode=%q", np.Mode)
	}
	if len(np.Rules) != 1 || len(np.Rules[0].Hosts) != 2 {
		t.Fatalf("rules=%#v", np.Rules)
	}
	if np.Rules[0].Hosts[0] != "208.80.154.232/32" || np.Rules[0].Hosts[1] != "192.168.1.0/24" {
		t.Fatalf("hosts=%#v", np.Rules[0].Hosts)
	}
	if np.DNS.Mode != sandbox.DNSAllowlist {
		t.Fatalf("dns mode=%q", np.DNS.Mode)
	}
	if len(np.DNS.Names) != 0 {
		t.Fatalf("dns names should be empty for CIDR-only, got %#v", np.DNS.Names)
	}
}

func TestResolveDomainAllowList(t *testing.T) {
	np, err := sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", "example.com, *.openai.com", nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkAllowlist {
		t.Fatalf("mode=%q", np.Mode)
	}
	if !np.EssentialServices {
		t.Fatal("expected essential_services")
	}
	if len(np.Rules) != 1 || len(np.Rules[0].Hosts) != 2 {
		t.Fatalf("rules=%#v", np.Rules)
	}
	if np.Rules[0].Hosts[0] != "example.com" || np.Rules[0].Hosts[1] != "*.openai.com" {
		t.Fatalf("hosts=%#v", np.Rules[0].Hosts)
	}
	if len(np.DNS.Names) != 2 || np.DNS.Names[1] != "*.openai.com" {
		t.Fatalf("dns names=%#v", np.DNS.Names)
	}
}

func TestResolveBlockAll(t *testing.T) {
	tru := true
	np, err := sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", "", &tru, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkBlockAll {
		t.Fatalf("mode=%q", np.Mode)
	}
	if np.DNS.Mode != sandbox.DNSNone {
		t.Fatalf("dns=%q", np.DNS.Mode)
	}

	fals := false
	np, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", "", &fals, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkAllowAll {
		t.Fatalf("mode=%q want allowall", np.Mode)
	}
	if len(np.Rules) != 0 {
		t.Fatalf("rules=%#v", np.Rules)
	}
}

func TestResolveAllowAll(t *testing.T) {
	tru := true
	np, err := sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", "", nil, &tru, false)
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkAllowAll {
		t.Fatalf("mode=%q", np.Mode)
	}
	if np.DNS.Mode != sandbox.DNSDenylist {
		t.Fatalf("dns=%q", np.DNS.Mode)
	}

	np, err = sandbox.ResolveNetworkPolicyFromProto(&cellarv1.NetworkPolicy{AllowAll: &tru})
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkAllowAll {
		t.Fatalf("mode=%q", np.Mode)
	}

	_, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", "example.com", nil, &tru, false)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive, got %v", err)
	}
}

func TestResolveMutualExclusion(t *testing.T) {
	tru := true
	_, err := sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "10.0.0.0/8", "example.com", nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive, got %v", err)
	}
	_, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "10.0.0.0/8", "", &tru, nil, false)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive, got %v", err)
	}
	_, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{
		Mode: sandbox.NetworkAllowlist,
		Rules: []sandbox.NetworkRule{{Hosts: []string{"x.com"}}},
	}, "10.0.0.0/8", "", nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("expected cannot combine, got %v", err)
	}
}

func TestResolveNetworkAllowListValidation(t *testing.T) {
	_, err := sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "208.80.154.232", "", nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "CIDR required") {
		t.Fatalf("expected CIDR required, got %v", err)
	}
	_, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "2001:db8::/32", "", nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "IPv4") {
		t.Fatalf("expected IPv4 only, got %v", err)
	}
	parts := make([]string, 11)
	for i := range parts {
		parts[i] = "10.0.0.0/32"
	}
	_, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, strings.Join(parts, ","), "", nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "max 10") {
		t.Fatalf("expected max 10, got %v", err)
	}
}

func TestResolveDomainAllowListValidation(t *testing.T) {
	_, err := sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", "https://example.com", nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "domains only") {
		t.Fatalf("expected domains only, got %v", err)
	}
	_, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", "example.com:443", nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "domains only") {
		t.Fatalf("expected domains only, got %v", err)
	}
	_, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", "10.0.0.0/8", nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "network_allow_list") {
		t.Fatalf("expected CIDR rejected, got %v", err)
	}
	parts := make([]string, 21)
	for i := range parts {
		parts[i] = "example.com"
	}
	_, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", strings.Join(parts, ","), nil, nil, false)
	if err == nil || !strings.Contains(err.Error(), "max 20") {
		t.Fatalf("expected max 20, got %v", err)
	}
}

func TestResolveNetworkPolicyFromProto(t *testing.T) {
	tru := true
	np, err := sandbox.ResolveNetworkPolicyFromProto(&cellarv1.NetworkPolicy{
		BlockAll:          &tru,
		EssentialServices: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkBlockAll {
		t.Fatalf("mode=%q", np.Mode)
	}
	if !np.EssentialServices {
		t.Fatal("expected essential")
	}

	np, err = sandbox.ResolveNetworkPolicyFromProto(&cellarv1.NetworkPolicy{
		DomainAllowList: "api.openai.com,*.github.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkAllowlist || len(np.Rules) != 1 {
		t.Fatalf("got mode=%q rules=%#v", np.Mode, np.Rules)
	}
}

func TestResolveEssentialServicesAloneImpliesBlockAll(t *testing.T) {
	np, err := sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{}, "", "", nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkBlockAll {
		t.Fatalf("mode=%q want blockall", np.Mode)
	}
	if !np.EssentialServices {
		t.Fatal("expected essential_services")
	}
	if np.DNS.Mode != sandbox.DNSNone {
		t.Fatalf("dns=%q", np.DNS.Mode)
	}

	// Mode none + essential_services (legacy CLI/YAML shape) also upgrades.
	np, err = sandbox.ResolveNetworkPolicyFromProto(&cellarv1.NetworkPolicy{
		Mode:              "none",
		EssentialServices: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkBlockAll {
		t.Fatalf("mode=%q want blockall", np.Mode)
	}

	// Structured allowlist + essentials must not be rewritten to blockall.
	np, err = sandbox.ResolveNetworkPolicy(sandbox.NetworkPolicy{
		Mode:              sandbox.NetworkAllowlist,
		EssentialServices: true,
		Rules:             []sandbox.NetworkRule{{Hosts: []string{"example.com"}}},
		DNS:               sandbox.DNSPolicy{Mode: sandbox.DNSAllowlist, Names: []string{"example.com"}},
	}, "", "", nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if np.Mode != sandbox.NetworkAllowlist {
		t.Fatalf("mode=%q want allowlist", np.Mode)
	}
}

func TestValidateNetworkPolicyBlockAll(t *testing.T) {
	err := sandbox.ValidateNetworkPolicy(sandbox.NetworkPolicy{Mode: sandbox.NetworkBlockAll})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIsEssentialHost(t *testing.T) {
	if !sandbox.IsEssentialHost("registry.npmjs.org") {
		t.Fatal("npm")
	}
	if !sandbox.IsEssentialHost("api.github.com") {
		t.Fatal("github subdomain")
	}
	if !sandbox.IsEssentialHost("proxy.golang.org") {
		t.Fatal("go proxy")
	}
	if sandbox.IsEssentialHost("evil.example.com") {
		t.Fatal("should not match")
	}
}

func TestNetworkPolicyProtoRoundTripEssential(t *testing.T) {
	in := sandbox.NetworkPolicy{
		Mode:              sandbox.NetworkAllowlist,
		EssentialServices: true,
		Rules:             []sandbox.NetworkRule{{Hosts: []string{"example.com"}}},
		DNS:               sandbox.DNSPolicy{Mode: sandbox.DNSAllowlist, Names: []string{"example.com"}},
	}
	out := sandbox.NetworkPolicyFromProto(sandbox.NetworkPolicyToProto(in))
	if !out.EssentialServices {
		t.Fatal("essential_services lost in round-trip")
	}
	if out.Mode != sandbox.NetworkAllowlist {
		t.Fatalf("mode=%q", out.Mode)
	}
}
