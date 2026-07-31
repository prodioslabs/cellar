package main

import (
	"fmt"
	"log"
	"os"

	"github.com/prodioslabs/cellar/internal/sandboxagent"
	"github.com/prodioslabs/cellar/internal/version"
)

func main() {
	args := os.Args[1:]
	if version.Requested(args) {
		fmt.Println(version.String())
		return
	}

	if handled, err := sandboxagent.RunJobCLI(args); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "cellar-agent: %v\n", err)
			os.Exit(1)
		}
		return
	}

	log.Printf("cellar-agent %s (pid 1 init)", version.Version)
	if err := sandboxagent.RunInit(); err != nil {
		log.Fatalf("cellar-agent: %v", err)
	}
}
