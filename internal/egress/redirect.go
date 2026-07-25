package egress

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"sync"
)

// RedirectManager installs iptables REDIRECT rules for sandbox egress.
// Requires root / CAP_NET_ADMIN. Failures are returned to the caller.
type RedirectManager struct {
	mu        sync.Mutex
	httpPort  int
	tlsPort   int
	otherPort int
	udpPort   int
	rules     map[string]string // sandboxID -> container iface or IP hint
}

func NewRedirectManager(httpPort, tlsPort, otherPort, udpPort int) *RedirectManager {
	return &RedirectManager{
		httpPort:  httpPort,
		tlsPort:   tlsPort,
		otherPort: otherPort,
		udpPort:   udpPort,
		rules:     make(map[string]string),
	}
}

// EnsureSandbox adds REDIRECT for traffic from the given source IP (container).
func (m *RedirectManager) EnsureSandbox(sandboxID, containerIP string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.rules[sandboxID]; ok && prev == containerIP {
		return nil
	}
	if prev, ok := m.rules[sandboxID]; ok {
		_ = m.deleteRules(prev)
		delete(m.rules, sandboxID)
	}
	if err := m.addRules(containerIP); err != nil {
		return err
	}
	m.rules[sandboxID] = containerIP
	return nil
}

// SeedSandbox records a sandbox IP without installing iptables rules.
// Used by unit tests that exercise RemoveSandbox / TeardownLocal.
func (m *RedirectManager) SeedSandbox(sandboxID, containerIP string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules[sandboxID] = containerIP
}

// HasSandbox reports whether REDIRECT rules are tracked for a sandbox.
func (m *RedirectManager) HasSandbox(sandboxID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rules[sandboxID]
	return ok
}

// RemoveSandbox deletes REDIRECT rules for a sandbox.
func (m *RedirectManager) RemoveSandbox(sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ip, ok := m.rules[sandboxID]
	if !ok {
		return nil
	}
	err := m.deleteRules(ip)
	delete(m.rules, sandboxID)
	return err
}

// Close removes all rules.
func (m *RedirectManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var first error
	for id, ip := range m.rules {
		if err := m.deleteRules(ip); err != nil && first == nil {
			first = err
		}
		delete(m.rules, id)
	}
	return first
}

// ruleSpecs returns the per-sandbox rules, ordered so the port-specific TCP
// rules are matched before the catch-all within each chain. Loopback is
// excluded so in-container local sockets are not redirected; the DNS bait
// address is not loopback, so UDP/53 needs no exclusion.
func (m *RedirectManager) ruleSpecs(srcIP string) [][]string {
	matches := [][]string{
		{"-p", "tcp", "--dport", "80", "!", "-d", "127.0.0.0/8", "-j", "REDIRECT", "--to-ports", strconv.Itoa(m.httpPort)},
		{"-p", "tcp", "--dport", "443", "!", "-d", "127.0.0.0/8", "-j", "REDIRECT", "--to-ports", strconv.Itoa(m.tlsPort)},
		{"-p", "tcp", "!", "-d", "127.0.0.0/8", "-j", "REDIRECT", "--to-ports", strconv.Itoa(m.otherPort)},
		{"-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-ports", strconv.Itoa(m.udpPort)},
	}
	var out [][]string
	for _, chain := range []string{"OUTPUT", "PREROUTING"} {
		for _, match := range matches {
			spec := append([]string{chain, "-s", srcIP}, match...)
			out = append(out, spec)
		}
	}
	return out
}

func (m *RedirectManager) addRules(srcIP string) error {
	if net.ParseIP(srcIP) == nil {
		return fmt.Errorf("invalid container ip %q", srcIP)
	}
	var applied [][]string
	for _, spec := range m.ruleSpecs(srcIP) {
		if err := runIPTables("-A", spec); err != nil {
			for _, done := range applied {
				_ = runIPTables("-D", done)
			}
			return err
		}
		applied = append(applied, spec)
	}
	return nil
}

func (m *RedirectManager) deleteRules(srcIP string) error {
	var first error
	for _, spec := range m.ruleSpecs(srcIP) {
		if err := runIPTables("-D", spec); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// runIPTables applies action ("-A" / "-D") to a nat-table rule spec whose first
// element is the chain.
func runIPTables(action string, spec []string) error {
	args := append([]string{"-t", "nat", action, spec[0]}, spec[1:]...)
	if out, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("iptables %v: %w (%s)", args, err, string(out))
	}
	return nil
}
