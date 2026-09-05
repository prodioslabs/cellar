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
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String())
		return
	}

	d := daemon.New(daemon.Config{
		DataDir:    *dataDir,
		SocketPath: *socket,
		ListenAddr: *listen,
		RaftAddr:   *raftAddr,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := d.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("cellard: %v", err)
	}
	fmt.Fprintln(os.Stderr, "cellard stopped")
}
