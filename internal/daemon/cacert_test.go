package daemon_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/daemon"
	"github.com/prodioslabs/cellar/pkg/client"
)

func TestCACertExport(t *testing.T) {
	base := t.TempDir()
	sock := filepath.Join(base, "mgr.sock")
	listen := freePort(t)
	raft := freePort(t)
	startDaemon(t, filepath.Join(base, "mgr"), sock, listen, raft)

	conn, err := daemon.DialLocal(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctrl := cellarv1.NewControlClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = ctrl.CACert(ctx, &cellarv1.CACertRequest{})
	if err == nil {
		t.Fatal("expected failure before init")
	}

	_, err = ctrl.Init(ctx, &cellarv1.InitRequest{
		AdvertiseAddr: listen,
		ListenAddr:    listen,
		RaftAddr:      raft,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	resp, err := ctrl.CACert(ctx, &cellarv1.CACertRequest{})
	if err != nil {
		t.Fatalf("ca-cert: %v", err)
	}
	if !strings.Contains(string(resp.Certificate), "BEGIN CERTIFICATE") {
		t.Fatalf("cert=%q", resp.Certificate)
	}

	envLine := client.FormatCACertEnv(resp.Certificate)
	val := strings.TrimPrefix(envLine, client.EnvCACert+`="`)
	val = strings.TrimSuffix(val, `"`)
	got, err := client.ResolveCACert(val)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(resp.Certificate) {
		t.Fatal("env round-trip mismatch")
	}
}
