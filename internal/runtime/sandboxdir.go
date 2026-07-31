package runtime

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
)

const (
	sandboxDirName   = "sandboxes"
	resolvConfName   = "resolv.conf"
	guestAgentBin    = "/usr/local/bin/cellar-agent"
	guestResolvConf  = "/etc/resolv.conf"
	defaultAgentPath = "/usr/lib/cellar/cellar-agent"
	stagedAgentName  = "cellar-agent"
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
	if v := os.Getenv("CELLAR_DATA_DIR"); v != "" {
		candidates = append(candidates, filepath.Join(v, stagedAgentName))
	}
	if home := agentInstallHomeDir(); home != "" {
		candidates = append(candidates, filepath.Join(home, ".cellar", stagedAgentName))
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

// agentInstallHomeDir returns the home used by installers for ~/.cellar staging.
// Matches install.sh / Makefile: prefer SUDO_USER when running as root.
func agentInstallHomeDir() string {
	if os.Geteuid() == 0 {
		if name := os.Getenv("SUDO_USER"); name != "" && name != "root" {
			if u, err := user.Lookup(name); err == nil && u.HomeDir != "" {
				return u.HomeDir
			}
		}
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	return ""
}

// StageAgentBinary copies cellar-agent into dataDir for Docker bind-mounts.
// Docker Desktop on macOS cannot bind-mount paths outside its shared directories
// (typically under the user home), so /usr/local/bin and Homebrew prefixes fail
// even when the file exists on the host. Staging under the platform data dir
// (~/.cellar on Darwin, /var/lib/cellar on Linux) keeps the mount source visible.
func StageAgentBinary(dataDir, src string) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("data dir required to stage cellar-agent")
	}
	if src == "" {
		return "", fmt.Errorf("cellar-agent source path is empty")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir data dir: %w", err)
	}

	dst := filepath.Join(dataDir, stagedAgentName)
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return "", fmt.Errorf("abs agent source: %w", err)
	}
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return "", fmt.Errorf("abs staged agent: %w", err)
	}
	if absSrc == absDst {
		return dst, nil
	}

	srcInfo, err := os.Stat(absSrc)
	if err != nil {
		return "", fmt.Errorf("stat cellar-agent: %w", err)
	}
	if dstInfo, err := os.Stat(absDst); err == nil &&
		dstInfo.Size() == srcInfo.Size() &&
		dstInfo.ModTime().Equal(srcInfo.ModTime()) &&
		dstInfo.Mode().Perm() == 0o755 {
		return dst, nil
	}

	tmp, err := os.CreateTemp(dataDir, "cellar-agent-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp agent: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	in, err := os.Open(absSrc)
	if err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("open cellar-agent: %w", err)
	}
	_, copyErr := io.Copy(tmp, in)
	_ = in.Close()
	closeErr := tmp.Close()
	if copyErr != nil {
		return "", fmt.Errorf("copy cellar-agent: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close staged cellar-agent: %w", closeErr)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", fmt.Errorf("chmod staged cellar-agent: %w", err)
	}
	if err := os.Chtimes(tmpName, srcInfo.ModTime(), srcInfo.ModTime()); err != nil {
		return "", fmt.Errorf("chtimes staged cellar-agent: %w", err)
	}
	if err := os.Rename(tmpName, absDst); err != nil {
		return "", fmt.Errorf("install staged cellar-agent: %w", err)
	}
	return dst, nil
}
