package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/node"
)

func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage cluster nodes",
	}
	cmd.AddCommand(newNodeListCmd())
	cmd.AddCommand(newNodeInspectCmd())
	cmd.AddCommand(newNodePromoteCmd())
	cmd.AddCommand(newNodeDemoteCmd())
	cmd.AddCommand(newNodeRemoveCmd())
	cmd.AddCommand(newNodeUpdateCmd())
	return cmd
}

func newNodeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List cluster nodes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			resp, err := client.NodeList(ctx, &cellarv1.NodeListRequest{})
			if err != nil {
				return fmt.Errorf("node ls: %w", err)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTYPE\tSTATUS\tAVAILABILITY\tMANAGER STATUS\tSANDBOXES")
			for _, n := range resp.Nodes {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					shortNodeID(n.NodeId),
					n.NodeType,
					n.Status,
					n.Availability,
					n.ManagerStatus,
					sandboxCountDisplay(n),
				)
			}
			return w.Flush()
		},
	}
}

func newNodeInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect NODE",
		Short: "Display detailed information on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			resp, err := client.NodeInspect(ctx, &cellarv1.NodeInspectRequest{NodeId: args[0]})
			if err != nil {
				return fmt.Errorf("node inspect: %w", err)
			}
			n := resp.Node
			if n == nil {
				return fmt.Errorf("empty response")
			}
			fmt.Printf("ID: %s\n", n.NodeId)
			fmt.Printf("Role: %s\n", n.Role)
			fmt.Printf("NodeType: %s\n", n.NodeType)
			fmt.Printf("Membership: %s\n", n.Membership)
			fmt.Printf("Availability: %s\n", n.Availability)
			fmt.Printf("Status: %s\n", n.Status)
			fmt.Printf("ManagerStatus: %s\n", n.ManagerStatus)
			fmt.Printf("RuntimeAddr: %s\n", n.RuntimeGrpcAddr)
			fmt.Printf("Sandboxes: %s\n", sandboxCountDisplay(n))
			if n.RuntimeHeartbeatUnixNano > 0 {
				fmt.Printf("Heartbeat: %s\n", time.Unix(0, n.RuntimeHeartbeatUnixNano).UTC().Format(time.RFC3339))
			}
			fmt.Printf("PubKeyFingerprint: %s\n", n.PubKeyFingerprint)
			if n.IssuedAtUnixNano > 0 {
				fmt.Printf("IssuedAt: %s\n", time.Unix(0, n.IssuedAtUnixNano).UTC().Format(time.RFC3339))
			}
			if n.ExpiresAtUnixNano > 0 {
				fmt.Printf("ExpiresAt: %s\n", time.Unix(0, n.ExpiresAtUnixNano).UTC().Format(time.RFC3339))
			}
			if len(n.Labels) > 0 {
				fmt.Printf("Labels:\n")
				for k, v := range n.Labels {
					fmt.Printf("  %s=%s\n", k, v)
				}
			}
			return nil
		},
	}
}

func newNodePromoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote NODE [NODE...]",
		Short: "Promote one or more nodes to manager (run on Raft leader)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			for _, id := range args {
				if _, err := client.NodePromote(ctx, &cellarv1.NodePromoteRequest{NodeId: id}); err != nil {
					return fmt.Errorf("node promote %s: %w", id, err)
				}
				fmt.Printf("Node %s promoted to manager.\n", id)
			}
			return nil
		},
	}
}

func newNodeDemoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "demote NODE [NODE...]",
		Short: "Demote one or more manager nodes to worker (run on Raft leader)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			for _, id := range args {
				if _, err := client.NodeDemote(ctx, &cellarv1.NodeDemoteRequest{NodeId: id}); err != nil {
					return fmt.Errorf("node demote %s: %w", id, err)
				}
				fmt.Printf("Node %s demoted to worker.\n", id)
			}
			return nil
		},
	}
}

func newNodeRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm NODE [NODE...]",
		Short: "Remove one or more nodes from the cluster (run on Raft leader)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			for _, id := range args {
				if _, err := client.NodeRemove(ctx, &cellarv1.NodeRemoveRequest{NodeId: id, Force: force}); err != nil {
					return fmt.Errorf("node rm %s: %w", id, err)
				}
				fmt.Printf("Node %s removed.\n", id)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "force remove a live node")
	return cmd
}

func newNodeUpdateCmd() *cobra.Command {
	var (
		availability string
		labelAdd     []string
		labelRm      []string
		role         string
	)
	cmd := &cobra.Command{
		Use:   "update NODE",
		Short: "Update node availability, labels, or role (run on Raft leader)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			adds, err := parseLabelPairs(labelAdd)
			if err != nil {
				return err
			}
			if availability == "" && role == "" && len(adds) == 0 && len(labelRm) == 0 {
				return fmt.Errorf("specify --availability, --label-add, --label-rm, and/or --role")
			}
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			resp, err := client.NodeUpdate(ctx, &cellarv1.NodeUpdateRequest{
				NodeId:       args[0],
				Availability: availability,
				LabelAdd:     adds,
				LabelRm:      labelRm,
				Role:         role,
			})
			if err != nil {
				return fmt.Errorf("node update: %w", err)
			}
			n := resp.Node
			fmt.Printf("Node %s updated (availability=%s role=%s).\n", shortNodeID(n.GetNodeId()), n.GetAvailability(), n.GetRole())
			return nil
		},
	}
	cmd.Flags().StringVar(&availability, "availability", "", "active | pause | drain")
	cmd.Flags().StringArrayVar(&labelAdd, "label-add", nil, "add label key=value (repeatable)")
	cmd.Flags().StringArrayVar(&labelRm, "label-rm", nil, "remove label key (repeatable)")
	cmd.Flags().StringVar(&role, "role", "", "worker | manager")
	return cmd
}

func parseLabelPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --label-add %q (want key=value)", p)
		}
		out[k] = v
	}
	return out, nil
}

func shortNodeID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// sandboxCountDisplay prints the last-reported sandbox count only when the
// node is live. Otherwise 0 is indistinguishable from "never heartbeated"
// and a stale count can outlive eviction.
func sandboxCountDisplay(n *cellarv1.NodeInfo) string {
	if n == nil || n.Status != string(node.StatusReady) {
		return "-"
	}
	return strconv.Itoa(int(n.RuntimeSandboxCount))
}
