package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

func newSandboxFsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fs",
		Short: "Read and write files inside a sandbox",
	}
	cmd.AddCommand(newSandboxFsReadCmd())
	cmd.AddCommand(newSandboxFsWriteCmd())
	cmd.AddCommand(newSandboxFsStatCmd())
	cmd.AddCommand(newSandboxFsListCmd())
	cmd.AddCommand(newSandboxFsMkdirCmd())
	cmd.AddCommand(newSandboxFsRemoveCmd())
	cmd.AddCommand(newSandboxFsRemoveDirCmd())
	cmd.AddCommand(newSandboxFsExistsCmd())
	cmd.AddCommand(newSandboxFsCopyCmd())
	cmd.AddCommand(newSandboxFsRenameCmd())
	return cmd
}

func newSandboxFsReadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read <sandbox-id> <path>",
		Short: "Read a file to stdout",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			stream, err := client.SandboxFsRead(cmd.Context(), &cellarv1.FsReadRequest{
				SandboxId: args[0],
				Path:      args[1],
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
				if len(chunk.Data) > 0 {
					_, _ = os.Stdout.Write(chunk.Data)
				}
			}
		},
	}
}

func newSandboxFsWriteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "write <sandbox-id> <path>",
		Short: "Write stdin to a file (create/overwrite)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			stream, err := client.SandboxFsWrite(cmd.Context())
			if err != nil {
				return err
			}
			if err := stream.Send(&cellarv1.FsWriteMessage{
				Payload: &cellarv1.FsWriteMessage_Start{Start: &cellarv1.FsWriteStart{
					SandboxId: args[0],
					Path:      args[1],
				}},
			}); err != nil {
				return err
			}
			buf := make([]byte, 32*1024)
			for {
				n, rerr := os.Stdin.Read(buf)
				if n > 0 {
					if err := stream.Send(&cellarv1.FsWriteMessage{
						Payload: &cellarv1.FsWriteMessage_Data{Data: append([]byte(nil), buf[:n]...)},
					}); err != nil {
						return err
					}
				}
				if rerr == io.EOF {
					_, err := stream.CloseAndRecv()
					return err
				}
				if rerr != nil {
					return rerr
				}
			}
		},
	}
}

func newSandboxFsStatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stat <sandbox-id> <path>",
		Short: "Print file metadata",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			resp, err := client.SandboxFsStat(cmd.Context(), &cellarv1.FsStatRequest{
				SandboxId: args[0],
				Path:      args[1],
			})
			if err != nil {
				return err
			}
			m := resp.Metadata
			if m == nil {
				return fmt.Errorf("empty metadata")
			}
			fmt.Printf("kind=%s size=%d mode=%04o readonly=%v", m.Kind, m.Size, m.Mode, m.Readonly)
			if m.ModifiedUnixNano != 0 {
				fmt.Printf(" modified=%s", time.Unix(0, m.ModifiedUnixNano).UTC().Format(time.RFC3339Nano))
			}
			if m.CreatedUnixNano != 0 {
				fmt.Printf(" created=%s", time.Unix(0, m.CreatedUnixNano).UTC().Format(time.RFC3339Nano))
			}
			fmt.Println()
			return nil
		},
	}
}

func newSandboxFsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <sandbox-id> <path>",
		Short: "List directory entries",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			resp, err := client.SandboxFsList(cmd.Context(), &cellarv1.FsListRequest{
				SandboxId: args[0],
				Path:      args[1],
			})
			if err != nil {
				return err
			}
			for _, e := range resp.Entries {
				fmt.Printf("%s\t%s\t%d\n", e.Kind, e.Path, e.Size)
			}
			return nil
		},
	}
}

func newSandboxFsMkdirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mkdir <sandbox-id> <path>",
		Short: "Create a directory (parents must exist)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			_, err = client.SandboxFsMkdir(cmd.Context(), &cellarv1.FsMkdirRequest{
				SandboxId: args[0],
				Path:      args[1],
			})
			return err
		},
	}
}

func newSandboxFsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <sandbox-id> <path>",
		Short: "Remove a file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			_, err = client.SandboxFsRemove(cmd.Context(), &cellarv1.FsRemoveRequest{
				SandboxId: args[0],
				Path:      args[1],
			})
			return err
		},
	}
}

func newSandboxFsRemoveDirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove-dir <sandbox-id> <path>",
		Short: "Remove an empty directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			_, err = client.SandboxFsRemoveDir(cmd.Context(), &cellarv1.FsRemoveDirRequest{
				SandboxId: args[0],
				Path:      args[1],
			})
			return err
		},
	}
}

func newSandboxFsExistsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exists <sandbox-id> <path>",
		Short: "Check whether a path exists",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			resp, err := client.SandboxFsExists(cmd.Context(), &cellarv1.FsExistsRequest{
				SandboxId: args[0],
				Path:      args[1],
			})
			if err != nil {
				return err
			}
			if resp.Exists {
				fmt.Println("true")
			} else {
				fmt.Println("false")
			}
			return nil
		},
	}
}

func newSandboxFsCopyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "copy <sandbox-id> <from> <to>",
		Short: "Copy a file within the sandbox",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			_, err = client.SandboxFsCopy(cmd.Context(), &cellarv1.FsCopyRequest{
				SandboxId: args[0],
				From:      args[1],
				To:        args[2],
			})
			return err
		},
	}
}

func newSandboxFsRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <sandbox-id> <from> <to>",
		Short: "Rename or move a path within the sandbox",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, closeFn, err := dial()
			if err != nil {
				return err
			}
			defer closeFn()
			_, err = client.SandboxFsRename(cmd.Context(), &cellarv1.FsRenameRequest{
				SandboxId: args[0],
				From:      args[1],
				To:        args[2],
			})
			return err
		},
	}
}
