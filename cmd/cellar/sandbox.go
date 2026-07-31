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
	cmd.AddCommand(newSandboxNetworkCmd())
	cmd.AddCommand(newSandboxListCmd())
	cmd.AddCommand(newSandboxLogsCmd())
	cmd.AddCommand(newSandboxExecCmd())
	cmd.AddCommand(newSandboxJobCmd())
	return cmd
}

func newSandboxCreateCmd() *cobra.Command {
	var (
		file              string
		image             string
		runtime           string
		name              string
		env               []string
		mounts            []string
		workdir           string
		memory            int64
		cpus              float64
		networkAllowList  string
		domainAllowList   string
		networkBlockAll   bool
		essentialServices bool
	)
	cmd := &cobra.Command{
		Use:   "create (--image <image> | --runtime <runtime> | -f <file>)",
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
				if image == "" && runtime == "" {
					return fmt.Errorf("--image or --runtime is required (or use -f <file>)")
				}
				if image != "" && runtime != "" {
					return fmt.Errorf("specify --image or --runtime, not both")
				}
				netPol, err := buildCreateNetworkPolicy(cmd, networkAllowList, domainAllowList, networkBlockAll, essentialServices)
				if err != nil {
					return err
				}
				spec := &cellarv1.SandboxSpec{
					Image:      image,
					Runtime:    runtime,
					Env:        env,
					WorkingDir: workdir,
					Resources: &cellarv1.Resources{
						MemoryBytes:  memory,
						CpuNanoCores: int64(cpus * 1e9),
					},
					Network: netPol,
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
	cmd.Flags().StringVar(&runtime, "runtime", "", "language runtime preset (node-26, bun-1.3, python-3.13, go-1.26)")
	cmd.Flags().StringVar(&name, "id", "", "optional sandbox id")
	cmd.Flags().StringArrayVar(&env, "env", nil, "KEY=VALUE")
	cmd.Flags().StringArrayVar(&mounts, "mount", nil, "src:dst[:ro]")
	cmd.Flags().StringVar(&workdir, "workdir", "", "working directory")
	cmd.Flags().Int64Var(&memory, "memory", 0, "memory bytes")
	cmd.Flags().Float64Var(&cpus, "cpus", 0, "CPU limit (e.g. 0.5)")
	cmd.Flags().StringVar(&networkAllowList, "network-allow-list", "", "comma-separated IPv4 CIDRs (max 10)")
	cmd.Flags().StringVar(&domainAllowList, "domain-allow-list", "", "comma-separated domains / *.wildcards (max 20)")
	cmd.Flags().BoolVar(&networkBlockAll, "network-block-all", false, "block all outbound traffic (keeps egress topology)")
	cmd.Flags().BoolVar(&essentialServices, "essential-services", false, "allow curated package/git/AI domains")
	return cmd
}

// buildCreateNetworkPolicy builds a NetworkPolicy from flat network-limit flags.
// With no limits set, the sandbox has no external network (mode none).
func buildCreateNetworkPolicy(cmd *cobra.Command, networkAllowList, domainAllowList string, networkBlockAll, essentialServices bool) (*cellarv1.NetworkPolicy, error) {
	limitsSet := (cmd.Flags().Changed("network-allow-list") && strings.TrimSpace(networkAllowList) != "") ||
		(cmd.Flags().Changed("domain-allow-list") && strings.TrimSpace(domainAllowList) != "") ||
		cmd.Flags().Changed("network-block-all")
	if !limitsSet {
		return &cellarv1.NetworkPolicy{Mode: "none", EssentialServices: essentialServices}, nil
	}
	limitCount := 0
	if strings.TrimSpace(networkAllowList) != "" {
		limitCount++
	}
	if strings.TrimSpace(domainAllowList) != "" {
		limitCount++
	}
	if cmd.Flags().Changed("network-block-all") {
		limitCount++
	}
	if limitCount > 1 {
		return nil, fmt.Errorf("--network-allow-list, --domain-allow-list, and --network-block-all are mutually exclusive")
	}
	np := &cellarv1.NetworkPolicy{
		NetworkAllowList:  strings.TrimSpace(networkAllowList),
		DomainAllowList:   strings.TrimSpace(domainAllowList),
		EssentialServices: essentialServices,
	}
	if cmd.Flags().Changed("network-block-all") {
		v := networkBlockAll
		np.BlockAll = &v
	}
	return np, nil
}

// rejectCreateFlagsWithFile errors if any create flag other than --file was set.
func rejectCreateFlagsWithFile(cmd *cobra.Command) error {
	exclusive := []string{
		"image", "runtime", "id", "env", "mount",
		"workdir", "memory", "cpus",
		"network-allow-list", "domain-allow-list", "network-block-all", "essential-services",
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

func newSandboxNetworkCmd() *cobra.Command {
	var (
		networkAllowList  string
		domainAllowList   string
		networkBlockAll   bool
		essentialServices bool
	)
	cmd := &cobra.Command{
		Use:   "network <sandbox-id>",
		Short: "Replace the network policy of a running sandbox",
		Long: "Replace the network policy of a running sandbox. Takes effect immediately, " +
			"closing established connections the new policy no longer allows.\n\n" +
			"Set one of --network-allow-list, --domain-allow-list, or --network-block-all " +
			"(mutually exclusive). Optional --essential-services allows curated package/git/AI domains.\n\n" +
			"Sandboxes created with no network (mode none) cannot gain egress later; recreate them instead. " +
			"block_all keeps egress topology and may be toggled live.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			netPol, err := buildUpdateNetworkPolicy(cmd, networkAllowList, domainAllowList, networkBlockAll, essentialServices)
			if err != nil {
				return err
			}

			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			resp, err := client.SandboxUpdateNetwork(ctx, &cellarv1.SandboxUpdateNetworkRequest{
				SandboxId: args[0],
				Network:   netPol,
			})
			if err != nil {
				return err
			}
			fmt.Printf("sandbox %s network policy updated (mode=%s)\n",
				resp.Sandbox.Id, resp.Sandbox.Spec.GetNetwork().GetMode())
			return nil
		},
	}
	cmd.Flags().StringVar(&networkAllowList, "network-allow-list", "", "comma-separated IPv4 CIDRs (max 10)")
	cmd.Flags().StringVar(&domainAllowList, "domain-allow-list", "", "comma-separated domains / *.wildcards (max 20)")
	cmd.Flags().BoolVar(&networkBlockAll, "network-block-all", false, "block all outbound (true) or open denylist (false)")
	cmd.Flags().BoolVar(&essentialServices, "essential-services", false, "allow curated package/git/AI domains")
	return cmd
}

func buildUpdateNetworkPolicy(cmd *cobra.Command, networkAllowList, domainAllowList string, networkBlockAll, essentialServices bool) (*cellarv1.NetworkPolicy, error) {
	limitsSet := (cmd.Flags().Changed("network-allow-list") && strings.TrimSpace(networkAllowList) != "") ||
		(cmd.Flags().Changed("domain-allow-list") && strings.TrimSpace(domainAllowList) != "") ||
		cmd.Flags().Changed("network-block-all")
	if !limitsSet {
		return nil, fmt.Errorf("set --network-allow-list, --domain-allow-list, or --network-block-all")
	}
	limitCount := 0
	if strings.TrimSpace(networkAllowList) != "" {
		limitCount++
	}
	if strings.TrimSpace(domainAllowList) != "" {
		limitCount++
	}
	if cmd.Flags().Changed("network-block-all") {
		limitCount++
	}
	if limitCount > 1 {
		return nil, fmt.Errorf("--network-allow-list, --domain-allow-list, and --network-block-all are mutually exclusive")
	}
	np := &cellarv1.NetworkPolicy{
		NetworkAllowList:  strings.TrimSpace(networkAllowList),
		DomainAllowList:   strings.TrimSpace(domainAllowList),
		EssentialServices: essentialServices,
	}
	if cmd.Flags().Changed("network-block-all") {
		v := networkBlockAll
		np.BlockAll = &v
	}
	return np, nil
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
			if sb.Spec.GetRuntime() != "" {
				fmt.Printf("runtime:  %s\n", sb.Spec.GetRuntime())
			}
			fmt.Printf("container: %s\n", sb.Status.GetContainerId())
			if sb.Status.GetMessage() != "" {
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
	var tty, detach bool
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
			if detach {
				resp, err := client.SandboxStartJob(cmd.Context(), &cellarv1.StartJobRequest{
					SandboxId: args[0],
					Command:   command,
				})
				if err != nil {
					return err
				}
				fmt.Println(resp.JobId)
				return nil
			}
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
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "run in background and print job id")
	return cmd
}

func newSandboxJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage background jobs in a sandbox",
	}
	cmd.AddCommand(newSandboxJobListCmd())
	cmd.AddCommand(newSandboxJobStopCmd())
	cmd.AddCommand(newSandboxJobLogsCmd())
	cmd.AddCommand(newSandboxJobGetCmd())
	return cmd
}

func newSandboxJobListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <sandbox-id>",
		Short: "List background jobs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			resp, err := client.SandboxListJobs(cmd.Context(), &cellarv1.ListJobsRequest{SandboxId: args[0]})
			if err != nil {
				return err
			}
			fmt.Printf("%-18s %-10s %-8s %s\n", "JOB", "PHASE", "EXIT", "COMMAND")
			for _, j := range resp.Jobs {
				fmt.Printf("%-18s %-10s %-8d %s\n", j.Id, j.Phase, j.ExitCode, strings.Join(j.Command, " "))
			}
			return nil
		},
	}
}

func newSandboxJobGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <sandbox-id> <job-id>",
		Short: "Inspect a background job",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			resp, err := client.SandboxGetJob(cmd.Context(), &cellarv1.GetJobRequest{
				SandboxId: args[0],
				JobId:     args[1],
			})
			if err != nil {
				return err
			}
			j := resp.Job
			fmt.Printf("id:      %s\nphase:   %s\nexit:    %d\ncommand: %s\n",
				j.Id, j.Phase, j.ExitCode, strings.Join(j.Command, " "))
			return nil
		},
	}
}

func newSandboxJobStopCmd() *cobra.Command {
	var timeout int32
	cmd := &cobra.Command{
		Use:   "stop <sandbox-id> <job-id>",
		Short: "Stop a background job",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			_, err = client.SandboxStopJob(cmd.Context(), &cellarv1.StopJobRequest{
				SandboxId:  args[0],
				JobId:      args[1],
				TimeoutSec: timeout,
			})
			return err
		},
	}
	cmd.Flags().Int32Var(&timeout, "timeout", 10, "seconds to wait before SIGKILL")
	return cmd
}

func newSandboxJobLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs <sandbox-id> <job-id>",
		Short: "Show background job logs",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			stream, err := client.SandboxJobLogs(cmd.Context(), &cellarv1.JobLogsRequest{
				SandboxId: args[0],
				JobId:     args[1],
				Follow:    follow,
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
	return cmd
}
