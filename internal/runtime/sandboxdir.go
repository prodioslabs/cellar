package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

const (
	sandboxDirName   = "sandboxes"
	agentSockName    = "agent.sock"
	agentTokenName   = "agent.token"
	resolvConfName   = "resolv.conf"
	guestAgentSock   = "/run/cellar/agent.sock"
	guestAgentBin    = "/usr/local/bin/cellar-agent"
	guestRunCellar   = "/run/cellar"
	guestResolvConf  = "/etc/resolv.conf"
	defaultAgentPath = "/usr/lib/cellar/cellar-agent"
)

// SandboxHostDir is the host directory for a sandbox's agent sock/token.
func SandboxHostDir(dataDir, sandboxID string) string {
	return filepath.Join(dataDir, sandboxDirName, sandboxID)
}

// AgentSockPath is the host path of the agent Unix socket.
func AgentSockPath(dataDir, sandboxID string) string {
	return filepath.Join(SandboxHostDir(dataDir, sandboxID), agentSockName)
}

// AgentTokenPath is the host path of the agent bearer token file.
func AgentTokenPath(dataDir, sandboxID string) string {
	return filepath.Join(SandboxHostDir(dataDir, sandboxID), agentTokenName)
}

// ResolvConfPath is the host path of the sandbox's generated resolv.conf.
func ResolvConfPath(dataDir, sandboxID string) string {
	return filepath.Join(SandboxHostDir(dataDir, sandboxID), resolvConfName)
}

// WriteEgressResolvConf writes the resolv.conf bind-mounted over the guest's own.
// Docker skips resolv.conf management when the path is mounted over, which is the
// only way to avoid the embedded 127.0.0.11 stub on user-defined networks.
// ndots:0 keeps search-domain expansion from flooding the egress resolver.
func WriteEgressResolvConf(dataDir, sandboxID, nameserver string) (string, error) {
	path := ResolvConfPath(dataDir, sandboxID)
	content := "nameserver " + nameserver + "\noptions ndots:0\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write resolv.conf: %w", err)
	}
	return path, nil
}

// PrepareSandboxDir creates the sandbox host dir and writes a fresh agent token.
func PrepareSandboxDir(dataDir, sandboxID string) (token string, err error) {
	// Keep the parent sandboxes/ directory owner-only so other host users cannot
	// list or enter per-sandbox dirs (which are world-accessible for gVisor).
	parent := filepath.Join(dataDir, sandboxDirName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("mkdir sandboxes dir: %w", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return "", fmt.Errorf("chmod sandboxes dir: %w", err)
	}

	dir := SandboxHostDir(dataDir, sandboxID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir sandbox dir: %w", err)
	}
	// gVisor's gofer enforces host POSIX perms without DAC_OVERRIDE for guest
	// root. cellard typically runs as non-root (systemd User=cellar), so a
	// 0700/0600 cellar-owned dir/token is unreadable/unwritable inside the
	// sandbox. World access on this leaf dir is gated by parent sandboxes/ 0700.
	if err := os.Chmod(dir, 0o777); err != nil {
		return "", fmt.Errorf("chmod sandbox dir: %w", err)
	}
	// Remove stale socket from a previous run.
	_ = os.Remove(filepath.Join(dir, agentSockName))

	token, err = mintAgentToken()
	if err != nil {
		return "", err
	}
	tokenPath := filepath.Join(dir, agentTokenName)
	if err := os.WriteFile(tokenPath, []byte(token), 0o644); err != nil {
		return "", fmt.Errorf("write agent token: %w", err)
	}
	return token, nil
}

// CleanupSandboxDir removes the host sandbox dir (sock + token).
func CleanupSandboxDir(dataDir, sandboxID string) error {
	if dataDir == "" || sandboxID == "" {
		return nil
	}
	return os.RemoveAll(SandboxHostDir(dataDir, sandboxID))
}

// ReadAgentToken reads the token file for a sandbox.
func ReadAgentToken(dataDir, sandboxID string) (string, error) {
	b, err := os.ReadFile(AgentTokenPath(dataDir, sandboxID))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func mintAgentToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mint agent token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ResolveAgentBinary returns the host path to cellar-agent.
func ResolveAgentBinary(explicit string) (string, error) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if v := os.Getenv("CELLAR_AGENT_BINARY"); v != "" {
		candidates = append(candidates, v)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "cellar-agent"))
	}
	candidates = append(candidates, defaultAgentPath)

	for _, c := range candidates {
		if c == "" {
			continue
		}
		st, err := os.Stat(c)
		if err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("cellar-agent binary not found (set CELLAR_AGENT_BINARY or install to %s)", defaultAgentPath)
}
