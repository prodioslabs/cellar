package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/prodioslabs/cellar/internal/daemon"
	"github.com/prodioslabs/cellar/internal/version"
)

func main() {
	dataDir := flag.String("data-dir", daemon.DefaultDataDir, "directory for node identity and raft state")
	socket := flag.String("socket", daemon.DefaultSocket, "unix socket for local control API")
	listen := flag.String("listen", daemon.DefaultListenAddr, "default remote gRPC listen address")
	raftAddr := flag.String("raft-addr", daemon.DefaultRaftAddr, "default raft TCP address")
	allowPrivate := flag.String("egress-allow-private-cidrs", "",
		"comma-separated CIDRs to exempt from the sandbox egress internal-range deny list")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String())
		return
	}

	d := daemon.New(daemon.Config{
		DataDir:            *dataDir,
		SocketPath:         *socket,
		ListenAddr:         *listen,
		RaftAddr:           *raftAddr,
		EgressAllowPrivate: splitCommaList(*allowPrivate),
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := d.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("cellard: %v", err)
	}
	fmt.Fprintln(os.Stderr, "cellard stopped")
}

func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
