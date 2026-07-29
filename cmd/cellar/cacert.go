package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/pkg/client"
)

func newCACertCmd() *cobra.Command {
	var (
		outPath string
		format  string
		envOut  bool
	)
	cmd := &cobra.Command{
		Use:   "ca-cert",
		Short: "Print or write the cluster CA certificate (public PEM)",
		Long: `Export the cluster Root CA certificate from the local cellard identity.

Useful for cellar-gateway (cluster CA to verify managers) and operational tooling.
Public HTTP SDKs talk to the gateway and do not need this cert. Never prints the CA private key.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			format = strings.ToLower(strings.TrimSpace(format))
			if format == "" {
				format = "pem"
			}
			if envOut && format == "base64" {
				return fmt.Errorf("--env and --format base64 are mutually exclusive")
			}
			if format != "pem" && format != "base64" {
				return fmt.Errorf("--format must be pem or base64")
			}

			c, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			resp, err := c.CACert(ctx, &cellarv1.CACertRequest{})
			if err != nil {
				return fmt.Errorf("ca-cert: %w", err)
			}
			pem := resp.Certificate
			if len(pem) == 0 {
				return fmt.Errorf("ca-cert: empty certificate")
			}

			var body []byte
			var mode os.FileMode = 0o644
			switch {
			case envOut:
				body = []byte(client.FormatCACertEnv(pem) + "\n")
				mode = 0o600
			case format == "base64":
				body = []byte(base64.StdEncoding.EncodeToString(pem) + "\n")
				mode = 0o600
			default:
				body = pem
				if len(body) > 0 && body[len(body)-1] != '\n' {
					body = append(body, '\n')
				}
			}

			if outPath != "" {
				if err := os.WriteFile(outPath, body, mode); err != nil {
					return fmt.Errorf("write %s: %w", outPath, err)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s\n", outPath)
				return nil
			}
			_, err = cmd.OutOrStdout().Write(body)
			return err
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "write output to this file instead of stdout")
	cmd.Flags().StringVar(&format, "format", "pem", "output format: pem | base64")
	cmd.Flags().BoolVar(&envOut, "env", false, `print CELLAR_CA_CERT="..." with \\n-escaped PEM for .env files`)
	return cmd
}
