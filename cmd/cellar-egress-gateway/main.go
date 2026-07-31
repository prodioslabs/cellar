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

	"github.com/prodioslabs/cellar/internal/egress/gateway"
	"github.com/prodioslabs/cellar/internal/version"
)

func main() {
	addr := flag.String("control-addr", "0.0.0.0:17948", "TCP listen address for gRPC control API")
	tokenFlag := flag.String("control-token", "", "Bearer token for control API (or CELLAR_EGRESS_CONTROL_TOKEN)")
	flag.Parse()
	if version.Requested(os.Args[1:]) {
		fmt.Println(version.String())
		return
	}

	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("CELLAR_EGRESS_CONTROL_TOKEN"))
	}
	if token == "" {
		log.Fatal("egress-gateway: control token required (-control-token or CELLAR_EGRESS_CONTROL_TOKEN)")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv := gateway.New()
	if err := srv.Start(ctx, gateway.ControlConfig{Addr: *addr, Token: token}); err != nil {
		log.Fatalf("egress-gateway: %v", err)
	}
	defer srv.Close()

	log.Printf("cellar-egress-gateway %s ready control=%s", version.Version, *addr)
	<-ctx.Done()
	log.Printf("egress-gateway shutting down")
}
