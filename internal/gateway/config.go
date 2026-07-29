package gateway

import (
	"fmt"
	"strings"
	"time"

	"github.com/prodioslabs/cellar/internal/daemon"
)

const (
	// DefaultListenAddr is the HTTP listen address for cellar-gateway.
	DefaultListenAddr = ":8080"
	// DefaultMaxBodyBytes caps JSON request bodies.
	DefaultMaxBodyBytes = 1 << 20 // 1 MiB
)

// Config configures the HTTP gateway.
type Config struct {
	// ListenAddr is the HTTP bind address (e.g. ":8080").
	ListenAddr string
	// DataDir is the cellard data directory used to load cluster CA and
	// advertised/manager addresses when Upstreams is empty.
	DataDir string
	// Upstreams are optional manager gRPC addresses (host:port). When set,
	// they override discovery from DataDir. Comma-separated values are
	// accepted via ParseUpstreams.
	Upstreams []string
	// MaxBodyBytes limits JSON request bodies (default 1 MiB).
	MaxBodyBytes int64
	// ReadyTimeout bounds readiness probes against SandboxAPI.
	ReadyTimeout time.Duration
}

// Normalize fills defaults and validates required fields.
func (c *Config) Normalize() error {
	if c.ListenAddr == "" {
		c.ListenAddr = DefaultListenAddr
	}
	if c.DataDir == "" {
		c.DataDir = daemon.DefaultDataDir
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if c.ReadyTimeout <= 0 {
		c.ReadyTimeout = 3 * time.Second
	}
	for i, u := range c.Upstreams {
		c.Upstreams[i] = strings.TrimSpace(u)
	}
	var cleaned []string
	for _, u := range c.Upstreams {
		if u != "" {
			cleaned = append(cleaned, u)
		}
	}
	c.Upstreams = cleaned
	if len(c.Upstreams) == 0 && c.DataDir == "" {
		return fmt.Errorf("data-dir or upstreams is required")
	}
	return nil
}

// ParseUpstreams splits a comma-separated upstream list.
func ParseUpstreams(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
