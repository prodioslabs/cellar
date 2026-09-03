package daemon

import (
	"context"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/grpcapi"
)

func (d *Daemon) SandboxFsRead(req *cellarv1.FsReadRequest, stream cellarv1.Control_SandboxFsReadServer) error {
	return d.withSandboxRuntime(stream.Context(), req.GetSandboxId(), func() error {
		rc, err := d.agent.FsRead(stream.Context(), req.SandboxId, req.Path)
		if err != nil {
			return grpcapi.MapFsErr(err)
		}
		defer rc.Close()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := rc.Read(buf)
			if n > 0 {
				if serr := stream.Send(&cellarv1.FsChunk{Data: append([]byte(nil), buf[:n]...)}); serr != nil {
					return serr
				}
			}
			if rerr == io.EOF {
				return nil
			}
			if rerr != nil {
				return grpcapi.MapFsErr(rerr)
			}
		}
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		remote, err := rt.FsRead(stream.Context(), req)
		if err != nil {
			return err
		}
		for {
			chunk, err := remote.Recv()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if err := stream.Send(chunk); err != nil {
				return err
			}
		}
	})
}

func (d *Daemon) SandboxFsWrite(stream cellarv1.Control_SandboxFsWriteServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be start")
	}
	return d.withSandboxRuntime(stream.Context(), start.GetSandboxId(), func() error {
		pr, pw := io.Pipe()
		errCh := make(chan error, 1)
		go func() {
			errCh <- d.agent.FsWrite(stream.Context(), start.SandboxId, start.Path, pr)
		}()
		if data := first.GetData(); len(data) > 0 {
			if _, err := pw.Write(data); err != nil {
				_ = pw.CloseWithError(err)
				<-errCh
				return err
			}
		}
		for {
			msg, rerr := stream.Recv()
			if rerr == io.EOF {
				_ = pw.Close()
				break
			}
			if rerr != nil {
				_ = pw.CloseWithError(rerr)
				<-errCh
				return rerr
			}
			data := msg.GetData()
			if len(data) == 0 {
				continue
			}
			if _, werr := pw.Write(data); werr != nil {
				<-errCh
				return grpcapi.MapFsErr(werr)
			}
		}
		if werr := <-errCh; werr != nil {
			return grpcapi.MapFsErr(werr)
		}
		return stream.SendAndClose(&cellarv1.FsWriteResponse{})
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		remote, err := rt.FsWrite(stream.Context())
		if err != nil {
			return err
		}
		if err := remote.Send(first); err != nil {
			return err
		}
		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				resp, err := remote.CloseAndRecv()
				if err != nil {
					return err
				}
				return stream.SendAndClose(resp)
			}
			if err != nil {
				return err
			}
			if err := remote.Send(msg); err != nil {
				return err
			}
		}
	})
}

func (d *Daemon) SandboxFsStat(ctx context.Context, req *cellarv1.FsStatRequest) (*cellarv1.FsStatResponse, error) {
	var resp *cellarv1.FsStatResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		meta, err := d.agent.FsStat(ctx, req.SandboxId, req.Path)
		if err != nil {
			return grpcapi.MapFsErr(err)
		}
		resp = &cellarv1.FsStatResponse{Metadata: grpcapi.ProtoFsMetadata(meta)}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.FsStat(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxFsList(ctx context.Context, req *cellarv1.FsListRequest) (*cellarv1.FsListResponse, error) {
	var resp *cellarv1.FsListResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		entries, err := d.agent.FsList(ctx, req.SandboxId, req.Path)
		if err != nil {
			return grpcapi.MapFsErr(err)
		}
		out := &cellarv1.FsListResponse{}
		for _, e := range entries {
			out.Entries = append(out.Entries, grpcapi.ProtoFsEntry(e))
		}
		resp = out
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.FsList(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxFsExists(ctx context.Context, req *cellarv1.FsExistsRequest) (*cellarv1.FsExistsResponse, error) {
	var resp *cellarv1.FsExistsResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		ok, err := d.agent.FsExists(ctx, req.SandboxId, req.Path)
		if err != nil {
			return grpcapi.MapFsErr(err)
		}
		resp = &cellarv1.FsExistsResponse{Exists: ok}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.FsExists(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxFsMkdir(ctx context.Context, req *cellarv1.FsMkdirRequest) (*cellarv1.FsMkdirResponse, error) {
	var resp *cellarv1.FsMkdirResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		if err := d.agent.FsMkdir(ctx, req.SandboxId, req.Path); err != nil {
			return grpcapi.MapFsErr(err)
		}
		resp = &cellarv1.FsMkdirResponse{}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.FsMkdir(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxFsRemove(ctx context.Context, req *cellarv1.FsRemoveRequest) (*cellarv1.FsRemoveResponse, error) {
	var resp *cellarv1.FsRemoveResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		if err := d.agent.FsRemove(ctx, req.SandboxId, req.Path); err != nil {
			return grpcapi.MapFsErr(err)
		}
		resp = &cellarv1.FsRemoveResponse{}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.FsRemove(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxFsRemoveDir(ctx context.Context, req *cellarv1.FsRemoveDirRequest) (*cellarv1.FsRemoveDirResponse, error) {
	var resp *cellarv1.FsRemoveDirResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		if err := d.agent.FsRemoveDir(ctx, req.SandboxId, req.Path); err != nil {
			return grpcapi.MapFsErr(err)
		}
		resp = &cellarv1.FsRemoveDirResponse{}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.FsRemoveDir(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxFsCopy(ctx context.Context, req *cellarv1.FsCopyRequest) (*cellarv1.FsCopyResponse, error) {
	var resp *cellarv1.FsCopyResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		if err := d.agent.FsCopy(ctx, req.SandboxId, req.From, req.To); err != nil {
			return grpcapi.MapFsErr(err)
		}
		resp = &cellarv1.FsCopyResponse{}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.FsCopy(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxFsRename(ctx context.Context, req *cellarv1.FsRenameRequest) (*cellarv1.FsRenameResponse, error) {
	var resp *cellarv1.FsRenameResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		if err := d.agent.FsRename(ctx, req.SandboxId, req.From, req.To); err != nil {
			return grpcapi.MapFsErr(err)
		}
		resp = &cellarv1.FsRenameResponse{}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.FsRename(ctx, req)
		return err
	})
	return resp, err
}
