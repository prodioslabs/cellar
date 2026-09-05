// Package paths holds platform default paths and local unix-socket dialing
// without importing the microsandbox runtime (keeps CLI/gateway CGO-free).
package paths

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	goruntime "runtime"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DefaultSocketPath returns the platform default control socket path.
func DefaultSocketPath() string {
	if goruntime.GOOS == "darwin" {
		if home := DarwinHomeDir(); home != "" {
			return filepath.Join(home, ".cellar", "cellar.sock")
		}
	}
	return "/var/run/cellar/cellar.sock"
}

// DefaultDataDirPath returns the platform default data directory.
func DefaultDataDirPath() string {
	if goruntime.GOOS == "darwin" {
		if home := DarwinHomeDir(); home != "" {
			return filepath.Join(home, ".cellar")
		}
	}
	return "/var/lib/cellar"
}

// DarwinHomeDir returns the macOS home directory for default paths.
// When started via sudo, prefer SUDO_USER's home over /var/root.
func DarwinHomeDir() string {
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

// DefaultSocket is resolved at init for the current platform.
var DefaultSocket = DefaultSocketPath()

// DefaultDataDir is resolved at init for the current platform.
var DefaultDataDir = DefaultDataDirPath()

// DialLocal connects to the local control socket.
func DialLocal(socketPath string) (*grpc.ClientConn, error) {
	if socketPath == "" {
		socketPath = DefaultSocket
	}
	abs, err := filepath.Abs(socketPath)
	if err != nil {
		return nil, fmt.Errorf("resolve socket path: %w", err)
	}
	// Resolve to an absolute path so unix:///… has an empty authority. Relative
	// paths like ./foo.sock become unix://./foo.sock, which gRPC rejects.
	return grpc.NewClient(
		"unix://"+abs,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", abs)
		}),
	)
}
