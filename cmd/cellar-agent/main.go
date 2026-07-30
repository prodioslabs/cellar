package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/prodioslabs/cellar/internal/sandboxagent"
	"github.com/prodioslabs/cellar/internal/version"
)

func main() {
	if version.Requested(os.Args[1:]) {
		fmt.Println(version.String())
		return
	}

	cfg, err := sandboxagent.LoadConfig()
	if err != nil {
		log.Fatalf("cellar-agent config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reapStop := make(chan struct{})
	go sandboxagent.ReapZombies(reapStop)
	defer close(reapStop)

	go func() {
		<-sandboxagent.NotifyShutdown()
		cancel()
	}()

	log.Printf("cellar-agent %s sandbox=%s sock=%s", version.Version, cfg.SandboxID, cfg.SockPath)
	if err := sandboxagent.ListenAndServe(ctx, cfg); err != nil {
		log.Fatalf("cellar-agent: %v", err)
	}
	os.Exit(0)
}
