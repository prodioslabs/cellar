package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewResolvesRelativePaths(t *testing.T) {
	d := New(Config{
		DataDir:    "./data-a",
		SocketPath: "./cellar-a.sock",
	})
	if !filepath.IsAbs(d.cfg.DataDir) {
		t.Fatalf("DataDir not absolute: %q", d.cfg.DataDir)
	}
	if filepath.Base(d.cfg.DataDir) != "data-a" {
		t.Fatalf("DataDir=%q, want base data-a", d.cfg.DataDir)
	}
	if !filepath.IsAbs(d.cfg.SocketPath) {
		t.Fatalf("SocketPath not absolute: %q", d.cfg.SocketPath)
	}
	if filepath.Base(d.cfg.SocketPath) != "cellar-a.sock" {
		t.Fatalf("SocketPath=%q, want base cellar-a.sock", d.cfg.SocketPath)
	}
}

func TestDarwinHomeDirNonRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Fatalf("UserHomeDir: %v %q", err, home)
	}
	if got := darwinHomeDir(); got != home {
		t.Fatalf("darwinHomeDir=%q, want %q", got, home)
	}
}
