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

// Evaluator applies NetworkPolicy in userspace.
type Evaluator struct {
	policy sandbox.NetworkPolicy
}

func NewEvaluator(policy sandbox.NetworkPolicy) *Evaluator {
	return &Evaluator{policy: policy}
}

// AllowConnect checks TCP connect to host (name or IP) and port.
func (e *Evaluator) AllowConnect(host string, port uint32) Decision {
	mode := e.policy.Mode
	if mode == "" || mode == sandbox.NetworkNone {
		return Deny
	}
	matched := e.matchRules(host, port)
	switch mode {
	case sandbox.NetworkAllowlist:
		if matched {
			return Allow
		}
		return Deny
	case sandbox.NetworkDenylist:
		if matched {
			return Deny
		}
		return Allow
	default:
		return Deny
	}
}

// AllowDNS checks whether a DNS name may be resolved.
func (e *Evaluator) AllowDNS(name string) Decision {
	mode := e.policy.DNS.Mode
	if mode == "" {
		mode = sandbox.DNSMode(e.policy.Mode)
	}
	if mode == "" || mode == sandbox.DNSNone {
		return Deny
	}
	matched := matchNames(name, e.policy.DNS.Names)
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

func (e *Evaluator) matchRules(host string, port uint32) bool {
	for _, r := range e.policy.Rules {
		if !hostMatchesAny(host, r.Hosts) {
			continue
		}
		if len(r.Ports) > 0 && !portIn(port, r.Ports) {
			continue
		}
		if len(r.Protocols) > 0 && !protoIn("tcp", r.Protocols) {
			continue
		}
		return true
	}
	return false
}

func (e *Evaluator) matchHostOnly(host string) bool {
	for _, r := range e.policy.Rules {
		if hostMatchesAny(host, r.Hosts) {
			return true
		}
	}
	return false
}

func hostMatchesAny(host string, patterns []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, p := range patterns {
		if hostMatch(host, p) {
			return true
		}
	}
	return false
}

func hostMatch(host, pattern string) bool {
	pattern = strings.TrimSpace(strings.ToLower(pattern))
	if pattern == "" {
		return false
	}
	if strings.Contains(pattern, "/") {
		ip := net.ParseIP(host)
		if ip == nil {
			return false
		}
		_, cidr, err := net.ParseCIDR(pattern)
		if err != nil {
			return false
		}
		return cidr.Contains(ip)
	}
	if ip := net.ParseIP(pattern); ip != nil {
		return strings.EqualFold(host, pattern) || net.ParseIP(host) != nil && net.ParseIP(host).Equal(ip)
	}
	if strings.HasPrefix(pattern, ".") {
		return strings.HasSuffix(host, pattern) || host == strings.TrimPrefix(pattern, ".")
	}
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}

func matchNames(name string, patterns []string) bool {
	return hostMatchesAny(name, patterns)
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
