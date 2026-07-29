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
	"github.com/prodioslabs/cellar/internal/gateway"
)

func main() {
	listen := flag.String("listen", gateway.DefaultListenAddr, "HTTP listen address")
	dataDir := flag.String("data-dir", daemon.DefaultDataDir, "cellard data directory for cluster CA and manager discovery")
	upstreams := flag.String("upstreams", "", "comma-separated manager gRPC addresses (overrides discovery from data-dir)")
	flag.Parse()

	cfg := gateway.Config{
		ListenAddr: *listen,
		DataDir:    *dataDir,
		Upstreams:  gateway.ParseUpstreams(*upstreams),
	}
	srv, err := gateway.New(cfg, nil)
	if err != nil {
		log.Fatalf("cellar-gateway: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("cellar-gateway: %v", err)
	}
	fmt.Fprintln(os.Stderr, "cellar-gateway stopped")
}
