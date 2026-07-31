package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/prodioslabs/cellar/internal/egress/gateway"
	"github.com/prodioslabs/cellar/internal/version"
)

func main() {
	sock := flag.String("control-sock", "/run/cellar/egress/control.sock", "Unix socket path for gRPC control API")
	flag.Parse()
	if version.Requested(os.Args[1:]) {
		fmt.Println(version.String())
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv := gateway.New()
	if err := srv.Start(ctx, *sock); err != nil {
		log.Fatalf("egress-gateway: %v", err)
	}
	defer srv.Close()

	log.Printf("cellar-egress-gateway %s ready", version.Version)
	<-ctx.Done()
	log.Printf("egress-gateway shutting down")
}
