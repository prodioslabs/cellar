package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/daemon"
)

var (
	socketPath    string
	advertiseAddr string
	listenAddr    string
	raftAddr      string
	joinToken     string
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "cellar",
		Short: "Cellar CLI — manage a local cellard daemon",
		Long: `cellar talks to a local cellard over a unix socket.

Start cellard first, then use init / join / leave / join-token / status.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&socketPath, "socket", daemon.DefaultSocket, "path to cellard control socket")

	root.AddCommand(newInitCmd())
	root.AddCommand(newJoinCmd())
	root.AddCommand(newLeaveCmd())
	root.AddCommand(newJoinTokenCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newSandboxCmd())
	root.AddCommand(newAPIKeyCmd())
	return root
}

func dial() (cellarv1.ControlClient, func(), error) {
	conn, err := daemon.DialLocal(socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("dial cellard: %w", err)
	}
	return cellarv1.NewControlClient(conn), func() { _ = conn.Close() }, nil
}

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new cluster on this node (first manager)",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			resp, err := client.Init(ctx, &cellarv1.InitRequest{
				AdvertiseAddr: advertiseAddr,
				ListenAddr:    listenAddr,
				RaftAddr:      raftAddr,
			})
			if err != nil {
				return fmt.Errorf("init: %w", err)
			}

			fmt.Printf("Cluster initialized: %s (node %s)\n\n", resp.ClusterId, resp.NodeId)
			fmt.Printf("To add a worker to this cluster, run the following command:\n\n")
			fmt.Printf("    cellar join --token %s %s\n\n", resp.WorkerToken, resp.AdvertiseAddr)
			fmt.Printf("To add a manager to this cluster, run the following command:\n\n")
			fmt.Printf("    cellar join --token %s %s\n", resp.ManagerToken, resp.AdvertiseAddr)
			return nil
		},
	}
	cmd.Flags().StringVar(&advertiseAddr, "advertise-addr", "", "address advertised to joining nodes (host:port)")
	cmd.Flags().StringVar(&listenAddr, "listen-addr", "", "remote gRPC listen address")
	cmd.Flags().StringVar(&raftAddr, "raft-addr", "", "raft TCP listen/advertise address")
	return cmd
}

func newJoinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join --token <token> <host:port>",
		Short: "Join this node to an existing cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if joinToken == "" {
				return fmt.Errorf("--token is required")
			}
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			resp, err := client.Join(ctx, &cellarv1.JoinRequest{
				Token:         joinToken,
				RemoteAddr:    args[0],
				AdvertiseAddr: advertiseAddr,
				ListenAddr:    listenAddr,
				RaftAddr:      raftAddr,
			})
			if err != nil {
				return fmt.Errorf("join: %w", err)
			}
			fmt.Printf("This node joined as a %s (%s).\n", resp.Role, resp.NodeId)
			return nil
		},
	}
	cmd.Flags().StringVar(&joinToken, "token", "", "cluster join token")
	cmd.Flags().StringVar(&advertiseAddr, "advertise-addr", "", "address this node advertises (managers)")
	cmd.Flags().StringVar(&listenAddr, "listen-addr", "", "remote gRPC listen address (managers)")
	cmd.Flags().StringVar(&raftAddr, "raft-addr", "", "raft TCP address (managers)")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

func newLeaveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "leave",
		Short: "Leave the cluster on this node",
		Long:  "Clears local membership so this node can init or join again. Managers require --force.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			_, err = client.Leave(ctx, &cellarv1.LeaveRequest{Force: force})
			if err != nil {
				return fmt.Errorf("leave: %w", err)
			}
			fmt.Println("Node left the cluster.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "force leave (required for managers)")
	return cmd
}

func newJoinTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "join-token {worker|manager}",
		Short:     "Print a ready-to-run join command for the given role",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"worker", "manager"},
		RunE: func(cmd *cobra.Command, args []string) error {
			role := strings.ToLower(args[0])
			if role != "worker" && role != "manager" {
				return fmt.Errorf("role must be worker or manager")
			}
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()

			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			resp, err := client.JoinToken(ctx, &cellarv1.JoinTokenRequest{Role: role})
			if err != nil {
				return fmt.Errorf("join-token: %w", err)
			}

			fmt.Printf("To add a %s to this cluster, run the following command:\n\n", role)
			fmt.Printf("    %s\n", resp.JoinCommand)
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show local node cluster status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			resp, err := client.Status(ctx, &cellarv1.StatusRequest{})
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}
			fmt.Printf("initialized: %v\n", resp.Initialized)
			fmt.Printf("node_id:     %s\n", resp.NodeId)
			fmt.Printf("role:        %s\n", resp.Role)
			fmt.Printf("cluster_id:  %s\n", resp.ClusterId)
			fmt.Printf("is_leader:   %v\n", resp.IsLeader)
			fmt.Printf("advertise:   %s\n", resp.AdvertiseAddr)
			return nil
		},
	}
}
