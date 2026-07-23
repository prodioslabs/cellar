package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage cluster sandboxes",
	}
	cmd.AddCommand(newSandboxCreateCmd())
	cmd.AddCommand(newSandboxStopCmd())
	cmd.AddCommand(newSandboxRemoveCmd())
	cmd.AddCommand(newSandboxGetCmd())
	cmd.AddCommand(newSandboxListCmd())
	cmd.AddCommand(newSandboxLogsCmd())
	cmd.AddCommand(newSandboxExecCmd())
	return cmd
}

func newSandboxCreateCmd() *cobra.Command {
	var (
		file       string
		image      string
		name       string
		network    string
		env        []string
		mounts     []string
		command    []string
		workdir    string
		memory     int64
		cpus       float64
		allowHosts []string
		allowPorts []int
	)
	cmd := &cobra.Command{
		Use:   "create (--image <image> | -f <file>)",
		Short: "Create and schedule a sandbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			var req *cellarv1.SandboxCreateRequest
			if file != "" {
				if err := rejectCreateFlagsWithFile(cmd); err != nil {
					return err
				}
				var err error
				req, err = loadSandboxCreateFile(file)
				if err != nil {
					return err
				}
			} else {
				if image == "" {
					return fmt.Errorf("--image is required (or use -f <file>)")
				}
				ports := make([]uint32, 0, len(allowPorts))
				for _, p := range allowPorts {
					ports = append(ports, uint32(p))
				}
				spec := &cellarv1.SandboxSpec{
					Image:      image,
					Env:        env,
					WorkingDir: workdir,
					Command:    command,
					Resources: &cellarv1.Resources{
						MemoryBytes:  memory,
						CpuNanoCores: int64(cpus * 1e9),
					},
					Network: networkPolicyFromAllow(network, allowHosts, ports),
				}
				for _, m := range mounts {
					parts := strings.SplitN(m, ":", 3)
					if len(parts) < 2 {
						return fmt.Errorf("invalid mount %q (want src:dst[:ro])", m)
					}
					ro := len(parts) == 3 && parts[2] == "ro"
					spec.Mounts = append(spec.Mounts, &cellarv1.Mount{
						Source:   parts[0],
						Target:   parts[1],
						ReadOnly: ro,
					})
				}
				req = &cellarv1.SandboxCreateRequest{Spec: spec, SandboxId: name}
			}

			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			resp, err := client.SandboxCreate(ctx, req)
			if err != nil {
				return err
			}
			fmt.Printf("sandbox %s created (node=%s phase=%s)\n",
				resp.Sandbox.Id, resp.Sandbox.NodeId, resp.Sandbox.Status.GetPhase())
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "YAML file with sandbox create config")
	cmd.Flags().StringVar(&image, "image", "", "container image")
	cmd.Flags().StringVar(&name, "id", "", "optional sandbox id")
	cmd.Flags().StringVar(&network, "network", "none", "none|allowlist|denylist")
	cmd.Flags().StringArrayVar(&env, "env", nil, "KEY=VALUE")
	cmd.Flags().StringArrayVar(&mounts, "mount", nil, "src:dst[:ro]")
	cmd.Flags().StringArrayVar(&command, "entrypoint", nil, "entrypoint override")
	cmd.Flags().StringVar(&workdir, "workdir", "", "working directory")
	cmd.Flags().Int64Var(&memory, "memory", 0, "memory bytes")
	cmd.Flags().Float64Var(&cpus, "cpus", 0, "CPU limit (e.g. 0.5)")
	cmd.Flags().StringArrayVar(&allowHosts, "allow-host", nil, "host/CIDR for network policy")
	cmd.Flags().IntSliceVar(&allowPorts, "allow-port", nil, "ports for network policy")
	return cmd
}

// rejectCreateFlagsWithFile errors if any create flag other than --file was set.
func rejectCreateFlagsWithFile(cmd *cobra.Command) error {
	exclusive := []string{
		"image", "id", "network", "env", "mount", "entrypoint",
		"workdir", "memory", "cpus", "allow-host", "allow-port",
	}
	var set []string
	for _, name := range exclusive {
		if cmd.Flags().Changed(name) {
			set = append(set, "--"+name)
		}
	}
	if len(set) > 0 {
		return fmt.Errorf("cannot combine --file with %s", strings.Join(set, ", "))
	}
	return nil
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
			sb := resp.Sandbox
			fmt.Printf("id:       %s\n", sb.Id)
			fmt.Printf("node:     %s\n", sb.NodeId)
			fmt.Printf("desired:  %s\n", sb.DesiredState)
			fmt.Printf("phase:    %s\n", sb.Status.GetPhase())
			fmt.Printf("image:    %s\n", sb.Spec.GetImage())
			fmt.Printf("container: %s\n", sb.Status.GetContainerId())
			if sb.Status.GetMessage() != "" {
				fmt.Printf("message:  %s\n", sb.Status.Message)
			}
			return nil
		},
	}
}

func newSandboxListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List sandboxes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			resp, err := client.SandboxList(ctx, &cellarv1.SandboxListRequest{})
			if err != nil {
				return err
			}
			fmt.Printf("%-36s %-12s %-12s %-10s %-10s %s\n",
				"ID", "NODE", "CONTAINER", "DESIRED", "PHASE", "IMAGE")
			for _, sb := range resp.Sandboxes {
				node := sb.NodeId
				if len(node) > 12 {
					node = node[:12]
				}
				cid := sb.Status.GetContainerId()
				if len(cid) > 12 {
					cid = cid[:12]
				}
				fmt.Printf("%-36s %-12s %-12s %-10s %-10s %s\n",
					sb.Id, node, cid, sb.DesiredState, sb.Status.GetPhase(), sb.Spec.GetImage())
			}
			return nil
		},
	}
}

func newSandboxLogsCmd() *cobra.Command {
	var follow bool
	var tail int64
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
				Tail:      tail,
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
				_, _ = os.Stdout.Write(chunk.Data)
			}
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().Int64Var(&tail, "tail", 0, "number of lines from the end (0=all)")
	return cmd
}

func newSandboxExecCmd() *cobra.Command {
	var tty bool
	cmd := &cobra.Command{
		Use:   "exec <sandbox-id> -- <command> [args...]",
		Short: "Run a command in a sandbox",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			command := args[1:]
			if len(command) == 0 {
				return fmt.Errorf("command required after sandbox id")
			}
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			stream, err := client.SandboxExec(cmd.Context())
			if err != nil {
				return err
			}
			if err := stream.Send(&cellarv1.SandboxExecMessage{
				Payload: &cellarv1.SandboxExecMessage_Start{Start: &cellarv1.SandboxExecStart{
					SandboxId: args[0],
					Command:   command,
					Tty:       tty,
					Stdin:     false,
				}},
			}); err != nil {
				return err
			}
			_ = stream.CloseSend()
			for {
				msg, err := stream.Recv()
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
				if b := msg.GetStdout(); len(b) > 0 {
					_, _ = os.Stdout.Write(b)
				}
				if b := msg.GetStderr(); len(b) > 0 {
					_, _ = os.Stderr.Write(b)
				}
				if ex := msg.GetExit(); ex != nil {
					if ex.Error != "" {
						return fmt.Errorf("%s", ex.Error)
					}
					if ex.ExitCode != 0 {
						os.Exit(int(ex.ExitCode))
					}
					return nil
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&tty, "tty", "t", false, "allocate a pseudo-TTY")
	return cmd
}
