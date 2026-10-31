package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/prodioslabs/cellar/internal/daemon"
	"github.com/prodioslabs/cellar/internal/version"
)

func main() {
	dataDir := flag.String("data-dir", daemon.DefaultDataDir, "directory for node identity and raft state")
	socket := flag.String("socket", daemon.DefaultSocket, "unix socket for local control API")
	listen := flag.String("listen", daemon.DefaultListenAddr, "default remote gRPC listen address")
	raftAddr := flag.String("raft-addr", daemon.DefaultRaftAddr, "default raft TCP address")
	snapshotDir := flag.String("snapshot-dir", os.Getenv("CELLAR_SNAPSHOT_DIR"), "local snapshot root (default: data-dir)")
	s3Bucket := flag.String("s3-bucket", os.Getenv("CELLAR_S3_BUCKET"), "optional S3 bucket for snapshot tars")
	s3Region := flag.String("s3-region", os.Getenv("CELLAR_S3_REGION"), "S3 region")
	s3Prefix := flag.String("s3-prefix", os.Getenv("CELLAR_S3_PREFIX"), "optional key prefix inside the bucket")
	s3Endpoint := flag.String("s3-endpoint", os.Getenv("CELLAR_S3_ENDPOINT"), "optional S3-compatible endpoint")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String())
		return
	}

	d := daemon.New(daemon.Config{
		DataDir:     *dataDir,
		SocketPath:  *socket,
		ListenAddr:  *listen,
		RaftAddr:    *raftAddr,
		SnapshotDir: *snapshotDir,
		S3Bucket:    *s3Bucket,
		S3Region:    *s3Region,
		S3Prefix:    *s3Prefix,
		S3Endpoint:  *s3Endpoint,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := d.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("cellard: %v", err)
	}
	fmt.Fprintln(os.Stderr, "cellard stopped")
}
