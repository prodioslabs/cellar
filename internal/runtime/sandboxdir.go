package runtime

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	sandboxDirName  = "sandboxes"
	resolvConfName  = "resolv.conf"
	guestAgentBin   = "/usr/local/bin/cellar-agent"
	guestResolvConf = "/etc/resolv.conf"
	defaultAgentPath = "/usr/lib/cellar/cellar-agent"
)

// SandboxHostDir is the host directory for a sandbox's resolv.conf / egress state / jobs.
func SandboxHostDir(dataDir, sandboxID string) string {
	return filepath.Join(dataDir, sandboxDirName, sandboxID)
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

// PrepareSandboxDir creates the sandbox host dir (resolv.conf, egress.json, jobs.json).
func PrepareSandboxDir(dataDir, sandboxID string) error {
	parent := filepath.Join(dataDir, sandboxDirName)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("mkdir sandboxes dir: %w", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return fmt.Errorf("chmod sandboxes dir: %w", err)
	}

	dir := SandboxHostDir(dataDir, sandboxID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir sandbox dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod sandbox dir: %w", err)
	}
	return nil
}

// CleanupSandboxDir removes the host sandbox dir.
func CleanupSandboxDir(dataDir, sandboxID string) error {
	if dataDir == "" || sandboxID == "" {
		return nil
	}
	return os.RemoveAll(SandboxHostDir(dataDir, sandboxID))
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
