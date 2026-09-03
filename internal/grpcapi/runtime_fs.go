package grpcapi

import (
	"context"
	"io"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/runtime"
)

// FsRuntime is the local filesystem surface used by SandboxRuntime.
type FsRuntime interface {
	FsRead(ctx context.Context, sandboxID, path string) (io.ReadCloser, error)
	FsWrite(ctx context.Context, sandboxID, path string, r io.Reader) error
	FsStat(ctx context.Context, sandboxID, path string) (*runtime.FsMetadata, error)
	FsList(ctx context.Context, sandboxID, path string) ([]runtime.FsEntry, error)
	FsExists(ctx context.Context, sandboxID, path string) (bool, error)
	FsMkdir(ctx context.Context, sandboxID, path string) error
	FsRemove(ctx context.Context, sandboxID, path string) error
	FsRemoveDir(ctx context.Context, sandboxID, path string) error
	FsCopy(ctx context.Context, sandboxID, from, to string) error
	FsRename(ctx context.Context, sandboxID, from, to string) error
}

func fsRuntime(r *RuntimeServer) (FsRuntime, error) {
	fr, ok := r.local.(FsRuntime)
	if !ok || r.local == nil {
		return nil, status.Error(codes.Unavailable, "runtime not ready")
	}
	return fr, nil
}

func MapFsErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "path must be absolute"),
		strings.Contains(lower, "path required"),
		strings.Contains(lower, "path must not contain"),
		strings.Contains(lower, "sandbox_id"):
		return status.Error(codes.InvalidArgument, msg)
	case strings.Contains(lower, "no such file"),
		strings.Contains(lower, "not found"):
		return status.Error(codes.NotFound, msg)
	case strings.Contains(lower, "file exists"),
		strings.Contains(lower, "already exists"):
		return status.Error(codes.AlreadyExists, msg)
	case strings.Contains(lower, "permission denied"):
		return status.Error(codes.PermissionDenied, msg)
	case strings.Contains(lower, "not a directory"),
		strings.Contains(lower, "is a directory"),
		strings.Contains(lower, "directory not empty"),
		strings.Contains(lower, "does not support directories"):
		return status.Error(codes.FailedPrecondition, msg)
	default:
		return status.Error(codes.Internal, msg)
	}
}

func requireSandboxPath(sandboxID, path string) error {
	if sandboxID == "" || path == "" {
		return status.Error(codes.InvalidArgument, "sandbox_id and path required")
	}
	return nil
}

func timeUnixNano(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func ProtoFsMetadata(m *runtime.FsMetadata) *cellarv1.FsMetadata {
	if m == nil {
		return nil
	}
	return &cellarv1.FsMetadata{
		Kind:             string(m.Kind),
		Size:             m.Size,
		Mode:             m.Mode,
		Readonly:         m.Readonly,
		ModifiedUnixNano: timeUnixNano(m.Modified),
		CreatedUnixNano:  timeUnixNano(m.Created),
	}
}

func ProtoFsEntry(e runtime.FsEntry) *cellarv1.FsEntry {
	return &cellarv1.FsEntry{
		Path:             e.Path,
		Kind:             string(e.Kind),
		Size:             e.Size,
		Mode:             e.Mode,
		ModifiedUnixNano: timeUnixNano(e.Modified),
	}
}

func (r *RuntimeServer) FsRead(req *cellarv1.FsReadRequest, stream cellarv1.SandboxRuntime_FsReadServer) error {
	fr, err := fsRuntime(r)
	if err != nil {
		return err
	}
	if err := requireSandboxPath(req.SandboxId, req.Path); err != nil {
		return err
	}
	rc, err := fr.FsRead(stream.Context(), req.SandboxId, req.Path)
	if err != nil {
		return MapFsErr(err)
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
			return MapFsErr(rerr)
		}
	}
}

func (r *RuntimeServer) FsWrite(stream cellarv1.SandboxRuntime_FsWriteServer) error {
	fr, err := fsRuntime(r)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be start")
	}
	if err := requireSandboxPath(start.SandboxId, start.Path); err != nil {
		return err
	}
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- fr.FsWrite(stream.Context(), start.SandboxId, start.Path, pr)
	}()
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
			return MapFsErr(werr)
		}
	}
	if werr := <-errCh; werr != nil {
		return MapFsErr(werr)
	}
	return stream.SendAndClose(&cellarv1.FsWriteResponse{})
}

func (r *RuntimeServer) FsStat(ctx context.Context, req *cellarv1.FsStatRequest) (*cellarv1.FsStatResponse, error) {
	fr, err := fsRuntime(r)
	if err != nil {
		return nil, err
	}
	if err := requireSandboxPath(req.SandboxId, req.Path); err != nil {
		return nil, err
	}
	meta, err := fr.FsStat(ctx, req.SandboxId, req.Path)
	if err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.FsStatResponse{Metadata: ProtoFsMetadata(meta)}, nil
}

func (r *RuntimeServer) FsList(ctx context.Context, req *cellarv1.FsListRequest) (*cellarv1.FsListResponse, error) {
	fr, err := fsRuntime(r)
	if err != nil {
		return nil, err
	}
	if err := requireSandboxPath(req.SandboxId, req.Path); err != nil {
		return nil, err
	}
	entries, err := fr.FsList(ctx, req.SandboxId, req.Path)
	if err != nil {
		return nil, MapFsErr(err)
	}
	out := &cellarv1.FsListResponse{}
	for _, e := range entries {
		out.Entries = append(out.Entries, ProtoFsEntry(e))
	}
	return out, nil
}

func (r *RuntimeServer) FsExists(ctx context.Context, req *cellarv1.FsExistsRequest) (*cellarv1.FsExistsResponse, error) {
	fr, err := fsRuntime(r)
	if err != nil {
		return nil, err
	}
	if err := requireSandboxPath(req.SandboxId, req.Path); err != nil {
		return nil, err
	}
	ok, err := fr.FsExists(ctx, req.SandboxId, req.Path)
	if err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.FsExistsResponse{Exists: ok}, nil
}

func (r *RuntimeServer) FsMkdir(ctx context.Context, req *cellarv1.FsMkdirRequest) (*cellarv1.FsMkdirResponse, error) {
	fr, err := fsRuntime(r)
	if err != nil {
		return nil, err
	}
	if err := requireSandboxPath(req.SandboxId, req.Path); err != nil {
		return nil, err
	}
	if err := fr.FsMkdir(ctx, req.SandboxId, req.Path); err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.FsMkdirResponse{}, nil
}

func (r *RuntimeServer) FsRemove(ctx context.Context, req *cellarv1.FsRemoveRequest) (*cellarv1.FsRemoveResponse, error) {
	fr, err := fsRuntime(r)
	if err != nil {
		return nil, err
	}
	if err := requireSandboxPath(req.SandboxId, req.Path); err != nil {
		return nil, err
	}
	if err := fr.FsRemove(ctx, req.SandboxId, req.Path); err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.FsRemoveResponse{}, nil
}

func (r *RuntimeServer) FsRemoveDir(ctx context.Context, req *cellarv1.FsRemoveDirRequest) (*cellarv1.FsRemoveDirResponse, error) {
	fr, err := fsRuntime(r)
	if err != nil {
		return nil, err
	}
	if err := requireSandboxPath(req.SandboxId, req.Path); err != nil {
		return nil, err
	}
	if err := fr.FsRemoveDir(ctx, req.SandboxId, req.Path); err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.FsRemoveDirResponse{}, nil
}

func (r *RuntimeServer) FsCopy(ctx context.Context, req *cellarv1.FsCopyRequest) (*cellarv1.FsCopyResponse, error) {
	fr, err := fsRuntime(r)
	if err != nil {
		return nil, err
	}
	if req.SandboxId == "" || req.From == "" || req.To == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id, from, and to required")
	}
	if err := fr.FsCopy(ctx, req.SandboxId, req.From, req.To); err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.FsCopyResponse{}, nil
}

func (r *RuntimeServer) FsRename(ctx context.Context, req *cellarv1.FsRenameRequest) (*cellarv1.FsRenameResponse, error) {
	fr, err := fsRuntime(r)
	if err != nil {
		return nil, err
	}
	if req.SandboxId == "" || req.From == "" || req.To == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id, from, and to required")
	}
	if err := fr.FsRename(ctx, req.SandboxId, req.From, req.To); err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.FsRenameResponse{}, nil
}
