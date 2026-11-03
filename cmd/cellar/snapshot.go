package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Inspect and delete filesystem snapshots",
	}
	cmd.AddCommand(newSnapshotListCmd())
	cmd.AddCommand(newSnapshotInspectCmd())
	cmd.AddCommand(newSnapshotRemoveCmd())
	return cmd
}

func printSnapshot(snap *cellarv1.FSSnapshot) {
	s := sandbox.FSSnapshotFromProto(snap)
	fmt.Printf("id:         %s\n", s.ID)
	fmt.Printf("sandbox:    %s (%s)\n", s.SandboxName, s.SandboxID)
	fmt.Printf("kind:       %s\n", s.Kind)
	fmt.Printf("status:     %s\n", s.Status)
	fmt.Printf("backend:    %s\n", s.Backend)
	fmt.Printf("location:   %s\n", s.Location)
	fmt.Printf("node:       %s\n", s.NodeID)
	if s.Digest != "" {
		fmt.Printf("digest:     %s\n", s.Digest)
	}
	if s.SizeBytes > 0 {
		fmt.Printf("size:       %d\n", s.SizeBytes)
	}
	if s.Message != "" {
		fmt.Printf("message:    %s\n", s.Message)
	}
	if !s.ExpiresAt.IsZero() {
		fmt.Printf("expires:    %s\n", s.ExpiresAt.UTC().Format(time.RFC3339))
	}
}

func newSnapshotListCmd() *cobra.Command {
	var sandboxName string
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List filesystem snapshots",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			resp, err := client.SnapshotList(ctx, &cellarv1.SnapshotListRequest{SandboxName: sandboxName})
			if err != nil {
				return err
			}
			if len(resp.Snapshots) == 0 {
				fmt.Println("No snapshots.")
				return nil
			}
			fmt.Printf("%-20s %-12s %-10s %-10s %s\n", "ID", "KIND", "STATUS", "BACKEND", "SANDBOX")
			for _, snap := range resp.Snapshots {
				id := snap.Id
				if len(id) > 16 {
					id = id[:16]
				}
				fmt.Printf("%-20s %-12s %-10s %-10s %s\n", id, snap.Kind, snap.Status, snap.Backend, snap.SandboxName)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sandboxName, "sandbox", "", "filter by sandbox name")
	return cmd
}

func newSnapshotInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <snapshot-id>",
		Short: "Show snapshot details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			resp, err := client.SnapshotGet(ctx, &cellarv1.SnapshotGetRequest{SnapshotId: args[0]})
			if err != nil {
				return err
			}
			printSnapshot(resp.Snapshot)
			return nil
		},
	}
}

func newSnapshotRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <snapshot-id>",
		Short: "Delete a filesystem snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			_, err = client.SnapshotDelete(ctx, &cellarv1.SnapshotDeleteRequest{SnapshotId: args[0]})
			return err
		},
	}
}

func newSandboxSnapshotCmd() *cobra.Command {
	var stop bool
	var expiration string
	cmd := &cobra.Command{
		Use:   "snapshot <id-or-name>",
		Short: "Create a filesystem snapshot (requires --stop)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stop {
				return fmt.Errorf("--stop is required")
			}
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			req := &cellarv1.SnapshotCreateRequest{SandboxId: args[0], Stop: true}
			if strings.TrimSpace(expiration) != "" {
				ms, err := sandbox.ParseExpirationMs(expiration)
				if err != nil {
					return err
				}
				req.ExpirationMs = &ms
			}
			resp, err := client.SnapshotCreate(ctx, req)
			if err != nil {
				return err
			}
			fmt.Printf("snapshot %s created (status=%s)\n", resp.Snapshot.Id, resp.Snapshot.Status)
			return nil
		},
	}
	cmd.Flags().BoolVar(&stop, "stop", false, "stop the sandbox before snapshotting (required)")
	cmd.Flags().StringVar(&expiration, "expiration", "", "snapshot TTL (14d, 0, or milliseconds)")
	return cmd
}

func newSandboxUpdateCmd() *cobra.Command {
	var expiration string
	var keepLast uint32
	cmd := &cobra.Command{
		Use:   "update <sandbox-id>",
		Short: "Update sandbox snapshot retention",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			req := &cellarv1.SandboxUpdateRequest{SandboxId: args[0]}
			if strings.TrimSpace(expiration) != "" {
				ms, err := sandbox.ParseExpirationMs(expiration)
				if err != nil {
					return err
				}
				req.SnapshotExpirationMs = &ms
			}
			if cmd.Flags().Changed("keep-last-snapshots") {
				req.KeepLastSnapshots = &keepLast
			}
			resp, err := client.SandboxUpdate(ctx, req)
			if err != nil {
				return err
			}
			fmt.Printf("sandbox %s updated\n", resp.Sandbox.Id)
			return nil
		},
	}
	cmd.Flags().StringVar(&expiration, "snapshot-expiration", "", "default snapshot TTL (7d, 0, or milliseconds)")
	cmd.Flags().Uint32Var(&keepLast, "keep-last-snapshots", 0, "keep the N newest snapshots (1-10)")
	return cmd
}
