package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

func newAPIKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api-key",
		Short: "Manage cluster API keys for the client SandboxAPI",
	}
	cmd.AddCommand(newAPIKeyCreateCmd())
	cmd.AddCommand(newAPIKeyListCmd())
	cmd.AddCommand(newAPIKeyDeleteCmd())
	return cmd
}

func newAPIKeyCreateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an API key (raw secret printed once; run on Raft leader)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			resp, err := client.APIKeyCreate(ctx, &cellarv1.APIKeyCreateRequest{Name: name})
			if err != nil {
				return fmt.Errorf("api-key create: %w", err)
			}
			fmt.Printf("API key created: %s (%s)\n\n", resp.Id, resp.Name)
			fmt.Printf("Store this secret now; it will not be shown again:\n\n")
			fmt.Printf("    %s\n\n", resp.Key)
			fmt.Printf("Export for the Go client:\n\n")
			fmt.Printf("    export CELLAR_API_KEY=%s\n", resp.Key)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "human-readable key name")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newAPIKeyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List API keys (masks only)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			resp, err := client.APIKeyList(ctx, &cellarv1.APIKeyListRequest{})
			if err != nil {
				return fmt.Errorf("api-key ls: %w", err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tMASK\tCREATED")
			for _, k := range resp.Keys {
				created := time.Unix(0, k.CreatedAtUnixNano).UTC().Format(time.RFC3339)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.Id, k.Name, k.Mask, created)
			}
			return w.Flush()
		},
	}
}

func newAPIKeyDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <id>",
		Aliases: []string{"delete"},
		Short:   "Delete an API key (run on Raft leader)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			_, err = client.APIKeyDelete(ctx, &cellarv1.APIKeyDeleteRequest{Id: args[0]})
			if err != nil {
				return fmt.Errorf("api-key rm: %w", err)
			}
			fmt.Println("API key deleted.")
			return nil
		},
	}
}
