package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/prodioslabs/cellar/internal/api"
	"github.com/prodioslabs/cellar/internal/store"
)

func main() {
	dataDir := flag.String("data-dir", "./cellar-data", "directory for CA and cluster state")
	listen := flag.String("listen", ":7946", "HTTP listen address")
	flag.Parse()

	fs, err := store.NewFileStore(*dataDir)
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	srv := api.New(fs)
	log.Printf("cellar CA listening on %s (data-dir=%s)", *listen, *dataDir)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe(*listen)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("server: %v", err)
		}
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "received %s, shutting down\n", sig)
	}
}
