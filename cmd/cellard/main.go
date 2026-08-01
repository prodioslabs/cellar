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
	"github.com/prodioslabs/cellar/internal/egress"
	"github.com/prodioslabs/cellar/internal/version"
)

func main() {
	dataDir := flag.String("data-dir", daemon.DefaultDataDir, "directory for node identity and raft state")
	socket := flag.String("socket", daemon.DefaultSocket, "unix socket for local control API")
	listen := flag.String("listen", daemon.DefaultListenAddr, "default remote gRPC listen address")
	raftAddr := flag.String("raft-addr", daemon.DefaultRaftAddr, "default raft TCP address")
	allowPrivate := flag.String("egress-allow-private-cidrs", "",
		"comma-separated CIDRs to exempt from the sandbox egress internal-range deny list")
	egressSupernet := flag.String("egress-supernet", egress.DefaultSupernet,
		"IPv4 supernet carved into /29s for per-sandbox internal networks")
	egressImage := flag.String("egress-gateway-image", egress.DefaultImage,
		"Docker image for the topology egress gateway")
	egressMaxLegs := flag.Int("egress-gateway-max-legs", egress.DefaultMaxLegs,
		"max concurrent sandbox network legs per egress gateway container")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String())
		return
	}

	d := daemon.New(daemon.Config{
		DataDir:              *dataDir,
		SocketPath:           *socket,
		ListenAddr:           *listen,
		RaftAddr:             *raftAddr,
		EgressAllowPrivate:   splitCommaList(*allowPrivate),
		EgressSupernet:       *egressSupernet,
		EgressGatewayImage:   *egressImage,
		EgressGatewayMaxLegs: *egressMaxLegs,
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
