package sandbox

import (
	"fmt"
	"net"
	"strings"
	"unicode"
)

const (
	maxNetworkAllowListEntries = 10
	maxDomainAllowListEntries  = 20
)

// ResolveNetworkPolicy translates Daytona-style sugar fields into a canonical
// NetworkPolicy, then normalizes and validates. Sugar fields and structured
// mode/rules/dns are mutually exclusive.
func ResolveNetworkPolicy(np NetworkPolicy, networkAllowList, domainAllowList string, blockAll *bool, essentialServices bool) (NetworkPolicy, error) {
	out := np
	out.EssentialServices = essentialServices || np.EssentialServices

	allowCIDRs := strings.TrimSpace(networkAllowList)
	allowDomains := strings.TrimSpace(domainAllowList)
	hasBlock := blockAll != nil
	sugarCount := 0
	if allowCIDRs != "" {
		sugarCount++
	}
	if allowDomains != "" {
		sugarCount++
	}
	if hasBlock && *blockAll {
		sugarCount++
	}
	// block_all:false alone is sugar that means "full open" (denylist, no rules).
	blockAllFalseAlone := hasBlock && !*blockAll && allowCIDRs == "" && allowDomains == ""

	if sugarCount > 1 {
		return NetworkPolicy{}, fmt.Errorf("network_allow_list, domain_allow_list, and block_all are mutually exclusive; set at most one")
	}
	if (sugarCount > 0 || blockAllFalseAlone) && hasStructuredNetwork(np) {
		return NetworkPolicy{}, fmt.Errorf("cannot combine network_allow_list/domain_allow_list/block_all with structured network mode, rules, or dns")
	}

	switch {
	case allowCIDRs != "":
		cidrs, err := parseNetworkAllowList(allowCIDRs)
		if err != nil {
			return NetworkPolicy{}, err
		}
		out.Mode = NetworkAllowlist
		out.Rules = []NetworkRule{{Hosts: cidrs, Protocols: []string{"tcp"}}}
		out.DNS = DNSPolicy{Mode: DNSAllowlist, Names: nil}
	case allowDomains != "":
		domains, err := parseDomainAllowList(allowDomains)
		if err != nil {
			return NetworkPolicy{}, err
		}
		out.Mode = NetworkAllowlist
		out.Rules = []NetworkRule{{Hosts: domains, Protocols: []string{"tcp"}}}
		out.DNS = DNSPolicy{Mode: DNSAllowlist, Names: append([]string(nil), domains...)}
	case hasBlock && *blockAll:
		out.Mode = NetworkBlockAll
		out.Rules = nil
		out.DNS = DNSPolicy{Mode: DNSNone}
	case blockAllFalseAlone:
		out.Mode = NetworkDenylist
		out.Rules = nil
		out.DNS = DNSPolicy{Mode: DNSDenylist}
	}

	out = NormalizeNetworkPolicy(out)
	if err := ValidateNetworkPolicy(out); err != nil {
		return NetworkPolicy{}, err
	}
	return out, nil
}

func hasStructuredNetwork(np NetworkPolicy) bool {
	if np.Mode != "" {
		return true
	}
	if np.DNS.Mode != "" || len(np.DNS.Names) > 0 {
		return true
	}
	return len(np.Rules) > 0
}

func parseNetworkAllowList(raw string) ([]string, error) {
	parts := splitCommaList(raw)
	if len(parts) == 0 {
		return nil, fmt.Errorf("network_allow_list is empty")
	}
	if len(parts) > maxNetworkAllowListEntries {
		return nil, fmt.Errorf("network_allow_list: max %d entries", maxNetworkAllowListEntries)
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if err := validateIPv4CIDR(p); err != nil {
			return nil, fmt.Errorf("network_allow_list: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

func parseDomainAllowList(raw string) ([]string, error) {
	parts := splitCommaList(raw)
	if len(parts) == 0 {
		return nil, fmt.Errorf("domain_allow_list is empty")
	}
	if len(parts) > maxDomainAllowListEntries {
		return nil, fmt.Errorf("domain_allow_list: max %d entries", maxDomainAllowListEntries)
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if err := validateDomainAllowEntry(p); err != nil {
			return nil, fmt.Errorf("domain_allow_list: %w", err)
		}
		out = append(out, strings.ToLower(p))
	}
	return out, nil
}

func splitCommaList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func validateIPv4CIDR(s string) error {
	if !strings.Contains(s, "/") {
		return fmt.Errorf("CIDR required (got %q); every entry must include a /prefix", s)
	}
	ip, ipNet, err := net.ParseCIDR(s)
	if err != nil {
		return fmt.Errorf("invalid CIDR %q", s)
	}
	if ip.To4() == nil {
		return fmt.Errorf("IPv4 only (got %q)", s)
	}
	ones, bits := ipNet.Mask.Size()
	if bits != 32 || ones < 0 || ones > 32 {
		return fmt.Errorf("invalid IPv4 prefix length in %q", s)
	}
	return nil
}

func validateDomainAllowEntry(s string) error {
	if s == "" {
		return fmt.Errorf("empty domain")
	}
	if strings.ContainsFunc(s, unicode.IsSpace) {
		return fmt.Errorf("invalid domain %q", s)
	}
	if net.ParseIP(s) != nil || strings.Contains(s, "/") {
		return fmt.Errorf("domains only (got address %q); use network_allow_list for CIDRs", s)
	}
	if strings.ContainsAny(s, ":?#") || strings.Contains(s, "://") {
		return fmt.Errorf("domains only (no protocol, path, port, or query): %q", s)
	}
	if strings.HasPrefix(s, "*.") {
		base := s[2:]
		if base == "" || strings.Contains(base, "*") {
			return fmt.Errorf("invalid wildcard domain %q", s)
		}
		return nil
	}
	if strings.Contains(s, "*") {
		return fmt.Errorf("invalid domain %q (use *.example.com for wildcards)", s)
	}
	return nil
}
