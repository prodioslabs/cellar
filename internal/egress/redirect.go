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
	mu      sync.Mutex
	tcpPort int
	udpPort int
	rules   map[string]string // sandboxID -> container iface or IP hint
}

func NewRedirectManager(tcpPort, udpPort int) *RedirectManager {
	return &RedirectManager{
		tcpPort: tcpPort,
		udpPort: udpPort,
		rules:   make(map[string]string),
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

func (m *RedirectManager) addRules(srcIP string) error {
	if net.ParseIP(srcIP) == nil {
		return fmt.Errorf("invalid container ip %q", srcIP)
	}
	tcp := strconv.Itoa(m.tcpPort)
	udp := strconv.Itoa(m.udpPort)
	cmds := [][]string{
		{"iptables", "-t", "nat", "-A", "OUTPUT", "-s", srcIP, "-p", "tcp", "!", "-d", "127.0.0.0/8", "-j", "REDIRECT", "--to-ports", tcp},
		{"iptables", "-t", "nat", "-A", "PREROUTING", "-s", srcIP, "-p", "tcp", "!", "-d", "127.0.0.0/8", "-j", "REDIRECT", "--to-ports", tcp},
		{"iptables", "-t", "nat", "-A", "PREROUTING", "-s", srcIP, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-ports", udp},
		{"iptables", "-t", "nat", "-A", "OUTPUT", "-s", srcIP, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-ports", udp},
	}
	for _, c := range cmds {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %w (%s)", c, err, string(out))
		}
	}
	return nil
}

func (m *RedirectManager) deleteRules(srcIP string) error {
	tcp := strconv.Itoa(m.tcpPort)
	udp := strconv.Itoa(m.udpPort)
	cmds := [][]string{
		{"iptables", "-t", "nat", "-D", "OUTPUT", "-s", srcIP, "-p", "tcp", "!", "-d", "127.0.0.0/8", "-j", "REDIRECT", "--to-ports", tcp},
		{"iptables", "-t", "nat", "-D", "PREROUTING", "-s", srcIP, "-p", "tcp", "!", "-d", "127.0.0.0/8", "-j", "REDIRECT", "--to-ports", tcp},
		{"iptables", "-t", "nat", "-D", "PREROUTING", "-s", srcIP, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-ports", udp},
		{"iptables", "-t", "nat", "-D", "OUTPUT", "-s", srcIP, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-ports", udp},
	}
	var first error
	for _, c := range cmds {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil && first == nil {
			first = fmt.Errorf("%v: %w (%s)", c, err, string(out))
		}
	}
	return first
}
