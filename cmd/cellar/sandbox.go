package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage cluster sandboxes",
	}
	cmd.AddCommand(newSandboxCreateCmd())
	cmd.AddCommand(newSandboxStartCmd())
	cmd.AddCommand(newSandboxStopCmd())
	cmd.AddCommand(newSandboxRemoveCmd())
	cmd.AddCommand(newSandboxGetCmd())
	cmd.AddCommand(newSandboxListCmd())
	cmd.AddCommand(newSandboxLogsCmd())
	return cmd
}

func newSandboxCreateCmd() *cobra.Command {
	var (
		name      string
		image     string
		runtime   string
		memoryMiB uint32
		vcpus     uint8
		start     bool
	)
	cmd := &cobra.Command{
		Use:   "create (--image <image> | --runtime <runtime>)",
		Short: "Create and schedule a sandbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("--name is required")
			}
			if image == "" && runtime == "" {
				return fmt.Errorf("--image or --runtime is required")
			}
			if image != "" && runtime != "" {
				return fmt.Errorf("specify --image or --runtime, not both")
			}

			spec := sandbox.Spec{
				Name: name,
				Resources: sandbox.Resources{
					VCPUs:     vcpus,
					MemoryMiB: memoryMiB,
				},
			}
			if image != "" {
				spec.Image = sandbox.OCIImage(image)
			}
			var err error
			spec, err = sandbox.ApplyLanguagePreset(spec, runtime)
			if err != nil {
				return err
			}
			spec = sandbox.NormalizeSpec(spec)
			if err := sandbox.ValidateSpec(spec); err != nil {
				return err
			}
			specJSON, err := sandbox.SpecToJSON(spec)
			if err != nil {
				return err
			}

			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			resp, err := client.SandboxCreate(ctx, &cellarv1.SandboxCreateRequest{
				SpecJson: specJSON,
				Start:    start,
			})
			if err != nil {
				return err
			}
			fmt.Printf("sandbox %s created (name=%s node=%s phase=%s)\n",
				resp.Sandbox.Id, resp.Sandbox.Name, resp.Sandbox.NodeId, resp.Sandbox.Status.GetPhase())
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "sandbox name (required)")
	cmd.Flags().StringVar(&image, "image", "", "OCI image reference")
	cmd.Flags().StringVar(&runtime, "runtime", "", "language runtime preset (node-26, bun-1.3, python-3.13, go-1.26)")
	cmd.Flags().Uint32Var(&memoryMiB, "memory-mib", 512, "memory limit in MiB")
	cmd.Flags().Uint8Var(&vcpus, "vcpus", 1, "number of vCPUs")
	cmd.Flags().BoolVar(&start, "start", false, "start the sandbox after create")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newSandboxStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <sandbox-id>",
		Short: "Start a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			resp, err := client.SandboxStart(ctx, &cellarv1.SandboxStartRequest{SandboxId: args[0]})
			if err != nil {
				return err
			}
			fmt.Printf("sandbox %s started (phase=%s)\n",
				resp.Sandbox.Id, resp.Sandbox.Status.GetPhase())
			return nil
		},
	}
}

func newSandboxStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <sandbox-id>",
		Short: "Stop a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			_, err = client.SandboxStop(ctx, &cellarv1.SandboxStopRequest{SandboxId: args[0]})
			return err
		},
	}
}

func newSandboxRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <sandbox-id>",
		Short: "Remove a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			_, err = client.SandboxRemove(ctx, &cellarv1.SandboxRemoveRequest{SandboxId: args[0]})
			return err
		},
	}
}

func newSandboxGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <sandbox-id>",
		Short: "Show sandbox details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			resp, err := client.SandboxGet(ctx, &cellarv1.SandboxGetRequest{SandboxId: args[0]})
			if err != nil {
				return err
			}
			sb := sandbox.FromProto(resp.Sandbox)
			fmt.Printf("id:       %s\n", sb.ID)
			fmt.Printf("name:     %s\n", sb.Name)
			fmt.Printf("node:     %s\n", sb.NodeID)
			fmt.Printf("desired:  %s\n", sb.DesiredState)
			fmt.Printf("phase:    %s\n", sb.Status.Phase)
			if ref := sb.Spec.ImageReference(); ref != "" {
				fmt.Printf("image:    %s\n", ref)
			}
			fmt.Printf("local:    %s\n", sb.Status.LocalName)
			if sb.Status.Message != "" {
				fmt.Printf("message:  %s\n", sb.Status.Message)
			}
			return nil
		},
	}
}

func newSandboxListCmd() *cobra.Command {
	var (
		all     bool
		nodeArg string
		filters []string
		format  string
	)
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List sandboxes",
		Long: "List sandboxes on the local node by default.\n\n" +
			"Use --all for every node in the cluster, or --node to target a specific node " +
			"(full id or unambiguous prefix). Repeatable --filter key=value narrows by " +
			"phase, desired, or image. Output formats: table (default), json, yaml.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && nodeArg != "" {
				return fmt.Errorf("cannot combine --all with --node")
			}
			filter, err := parseSandboxFilters(filters)
			if err != nil {
				return err
			}

			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			resp, err := client.SandboxList(ctx, &cellarv1.SandboxListRequest{})
			if err != nil {
				return fmt.Errorf("sandbox ls: %w", err)
			}
			sandboxes := resp.Sandboxes

			switch {
			case all:
				// cluster-wide
			case nodeArg != "":
				nodesResp, err := client.NodeList(ctx, &cellarv1.NodeListRequest{})
				if err != nil {
					return fmt.Errorf("sandbox ls: resolve node: %w", err)
				}
				nodeID, err := resolveNodeIDPrefix(nodesResp.Nodes, nodeArg)
				if err != nil {
					return fmt.Errorf("sandbox ls: %w", err)
				}
				sandboxes = filterSandboxesByNode(sandboxes, nodeID)
			default:
				st, err := client.Status(ctx, &cellarv1.StatusRequest{})
				if err != nil {
					return fmt.Errorf("sandbox ls: status: %w", err)
				}
				if !st.Initialized || st.NodeId == "" {
					return fmt.Errorf("sandbox ls: local node is not initialized; use --all or join a cluster first")
				}
				sandboxes = filterSandboxesByNode(sandboxes, st.NodeId)
			}

			sandboxes = applySandboxFilters(sandboxes, filter)
			if err := writeSandboxList(cmd.OutOrStdout(), format, sandboxes); err != nil {
				return fmt.Errorf("sandbox ls: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list sandboxes on all nodes")
	cmd.Flags().StringVar(&nodeArg, "node", "", "list sandboxes on a specific node (id or unambiguous prefix)")
	cmd.Flags().StringArrayVar(&filters, "filter", nil, "filter key=value (phase, desired, image; repeatable)")
	cmd.Flags().StringVar(&format, "format", "table", "output format: table|json|yaml")
	return cmd
}

func newSandboxLogsCmd() *cobra.Command {
	var follow bool
	var sources string
	cmd := &cobra.Command{
		Use:   "logs <sandbox-id>",
		Short: "Stream sandbox logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			stream, err := client.SandboxLogs(cmd.Context(), &cellarv1.SandboxLogsRequest{
				SandboxId: args[0],
				Follow:    follow,
				Sources:   sources,
			})
			if err != nil {
				return err
			}
			for {
				chunk, err := stream.Recv()
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
				if chunk.Text != "" {
					_, _ = io.WriteString(os.Stdout, chunk.Text)
					if !strings.HasSuffix(chunk.Text, "\n") {
						_, _ = io.WriteString(os.Stdout, "\n")
					}
					continue
				}
				// Fallback: encode the chunk as JSON if text is empty.
				b, _ := json.Marshal(chunk)
				_, _ = os.Stdout.Write(append(b, '\n'))
			}
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().StringVar(&sources, "sources", "", "comma-separated log sources (stdout,stderr,system,output)")
	return cmd
}
