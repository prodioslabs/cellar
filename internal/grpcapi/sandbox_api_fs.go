package grpcapi

import (
	"context"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

func (s *SandboxAPIServer) FsRead(req *cellarv1.FsReadRequest, stream cellarv1.SandboxAPI_FsReadServer) error {
	rt, closeFn, err := s.dialOwningRuntime(stream.Context(), req.GetSandboxId())
	if err != nil {
		return err
	}
	defer closeFn()
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
}

func (s *SandboxAPIServer) FsWrite(stream cellarv1.SandboxAPI_FsWriteServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be start")
	}
	rt, closeFn, err := s.dialOwningRuntime(stream.Context(), start.GetSandboxId())
	if err != nil {
		return err
	}
	defer closeFn()
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
}

func (s *SandboxAPIServer) FsStat(ctx context.Context, req *cellarv1.FsStatRequest) (*cellarv1.FsStatResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.FsStat(ctx, req)
}

func (s *SandboxAPIServer) FsList(ctx context.Context, req *cellarv1.FsListRequest) (*cellarv1.FsListResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.FsList(ctx, req)
}

func (s *SandboxAPIServer) FsExists(ctx context.Context, req *cellarv1.FsExistsRequest) (*cellarv1.FsExistsResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.FsExists(ctx, req)
}

func (s *SandboxAPIServer) FsMkdir(ctx context.Context, req *cellarv1.FsMkdirRequest) (*cellarv1.FsMkdirResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.FsMkdir(ctx, req)
}

func (s *SandboxAPIServer) FsRemove(ctx context.Context, req *cellarv1.FsRemoveRequest) (*cellarv1.FsRemoveResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.FsRemove(ctx, req)
}

func (s *SandboxAPIServer) FsRemoveDir(ctx context.Context, req *cellarv1.FsRemoveDirRequest) (*cellarv1.FsRemoveDirResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.FsRemoveDir(ctx, req)
}

func (s *SandboxAPIServer) FsCopy(ctx context.Context, req *cellarv1.FsCopyRequest) (*cellarv1.FsCopyResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.FsCopy(ctx, req)
}

func (s *SandboxAPIServer) FsRename(ctx context.Context, req *cellarv1.FsRenameRequest) (*cellarv1.FsRenameResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.FsRename(ctx, req)
}
