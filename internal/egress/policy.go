package egress

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

// Decision is the result of evaluating a connect or DNS query.
type Decision bool

const (
	Allow Decision = true
	Deny  Decision = false
)

// MatchType records how a connection matched, which decides how it is dialed:
// a domain match is re-dialed by name so the destination cannot be swapped for
// an internal address, anything else uses the original destination IP.
type MatchType string

const (
	MatchNone   MatchType = "none"
	MatchDomain MatchType = "domain"
	MatchCIDR   MatchType = "cidr"
)

// Evaluator applies NetworkPolicy in userspace.
type Evaluator struct {
	policy sandbox.NetworkPolicy
}

func NewEvaluator(policy sandbox.NetworkPolicy) *Evaluator {
	return &Evaluator{policy: policy}
}

// Policy returns the evaluated policy.
func (e *Evaluator) Policy() sandbox.NetworkPolicy { return e.policy }

// AllowConnect checks a TCP connect. hostname is the name asserted by the
// connection itself (TLS SNI or HTTP Host) and is empty when unknown; ip is the
// original destination. Hostname rules therefore only apply to the ports where
// a name is observable, everything else falls back to IP and CIDR rules.
func (e *Evaluator) AllowConnect(hostname string, ip net.IP, port uint32) (Decision, MatchType) {
	mode := e.policy.Mode
	if mode == "" || mode == sandbox.NetworkNone {
		return Deny, MatchNone
	}
	if mode == sandbox.NetworkBlockAll {
		if e.policy.EssentialServices && hostname != "" && sandbox.IsEssentialHost(hostname) {
			return Allow, MatchDomain
		}
		return Deny, MatchNone
	}
	if e.policy.EssentialServices && hostname != "" && sandbox.IsEssentialHost(hostname) {
		return Allow, MatchDomain
	}
	match := e.match(hostname, ip, port)
	switch mode {
	case sandbox.NetworkAllowlist:
		if match != MatchNone {
			return Allow, match
		}
		return Deny, MatchNone
	case sandbox.NetworkDenylist:
		if match != MatchNone {
			return Deny, match
		}
		// Nothing was asserted about this destination, so keep the guest's
		// original target rather than re-resolving the name.
		return Allow, MatchNone
	default:
		return Deny, MatchNone
	}
}

// match reports the strongest match for a destination, preferring a domain
// match so the caller can re-dial by name.
func (e *Evaluator) match(hostname string, ip net.IP, port uint32) MatchType {
	if hostname != "" {
		for _, r := range e.policy.Rules {
			if ruleAppliesTo(r, port) && nameMatchesAny(hostname, r.Hosts) {
				return MatchDomain
			}
		}
	}
	if ip != nil {
		for _, r := range e.policy.Rules {
			if ruleAppliesTo(r, port) && ipMatchesAny(ip, r.Hosts) {
				return MatchCIDR
			}
		}
	}
	return MatchNone
}

// AllowDNS checks whether a DNS name may be resolved.
func (e *Evaluator) AllowDNS(name string) Decision {
	mode := e.policy.DNS.Mode
	if mode == "" {
		mode = sandbox.DNSMode(e.policy.Mode)
	}
	if e.policy.Mode == sandbox.NetworkBlockAll {
		if e.policy.EssentialServices && sandbox.IsEssentialHost(name) {
			return Allow
		}
		return Deny
	}
	if mode == "" || mode == sandbox.DNSNone {
		return Deny
	}
	if e.policy.EssentialServices && sandbox.IsEssentialHost(name) {
		return Allow
	}
	matched := nameMatchesAny(name, e.policy.DNS.Names)
	// If DNS names empty, fall back to rule hosts for allow/deny symmetry.
	if len(e.policy.DNS.Names) == 0 {
		matched = e.matchHostOnly(name)
	}
	switch mode {
	case sandbox.DNSAllowlist:
		if matched {
			return Allow
		}
		return Deny
	case sandbox.DNSDenylist:
		if matched {
			return Deny
		}
		return Allow
	default:
		return Deny
	}
}

func (e *Evaluator) matchHostOnly(host string) bool {
	for _, r := range e.policy.Rules {
		if nameMatchesAny(host, r.Hosts) {
			return true
		}
	}
	return false
}

func ruleAppliesTo(r sandbox.NetworkRule, port uint32) bool {
	if len(r.Ports) > 0 && !portIn(port, r.Ports) {
		return false
	}
	if len(r.Protocols) > 0 && !protoIn("tcp", r.Protocols) {
		return false
	}
	return true
}

func nameMatchesAny(host string, patterns []string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return false
	}
	for _, p := range patterns {
		if nameMatch(host, normalizePattern(p)) {
			return true
		}
	}
	return false
}

// nameMatch matches a hostname against a name pattern. IP and CIDR patterns
// never match a name.
func nameMatch(host, pattern string) bool {
	switch {
	case pattern == "":
		return false
	case pattern == "*":
		return true
	case isAddrPattern(pattern):
		return false
	case strings.HasPrefix(pattern, "*."):
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(host, suffix) || host == suffix[1:]
	case strings.HasPrefix(pattern, "."):
		return strings.HasSuffix(host, pattern) || host == pattern[1:]
	default:
		return host == pattern || strings.HasSuffix(host, "."+pattern)
	}
}

func ipMatchesAny(ip net.IP, patterns []string) bool {
	for _, p := range patterns {
		if ipMatch(ip, normalizePattern(p)) {
			return true
		}
	}
	return false
}

// ipMatch matches a destination address against an IP or CIDR pattern. Name
// patterns never match an address.
func ipMatch(ip net.IP, pattern string) bool {
	if ip == nil || pattern == "" {
		return false
	}
	if strings.Contains(pattern, "/") {
		_, cidr, err := net.ParseCIDR(pattern)
		if err != nil {
			return false
		}
		return cidr.Contains(ip)
	}
	if lit := net.ParseIP(pattern); lit != nil {
		return lit.Equal(ip)
	}
	return false
}

func isAddrPattern(pattern string) bool {
	return strings.Contains(pattern, "/") || net.ParseIP(pattern) != nil
}

func normalizePattern(p string) string {
	return strings.TrimSpace(strings.ToLower(p))
}

func portIn(port uint32, ports []uint32) bool {
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

func protoIn(proto string, list []string) bool {
	proto = strings.ToLower(proto)
	for _, p := range list {
		if strings.EqualFold(p, proto) {
			return true
		}
	}
	return false
}

// ParseHostPort splits host:port.
func ParseHostPort(addr string) (string, uint32, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	p, err := strconv.ParseUint(portStr, 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port: %w", err)
	}
	return host, uint32(p), nil
}
