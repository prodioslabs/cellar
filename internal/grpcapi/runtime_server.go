//go:build cgo

package grpcapi

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/runtime"
)

// RuntimeServer implements SandboxRuntime on every node.
type RuntimeServer struct {
	cellarv1.UnimplementedSandboxRuntimeServer

	driver *runtime.Driver

	mu          sync.Mutex
	volumeNames map[string]string // cellar volume id -> local msb name
}

// NewRuntimeServer constructs a SandboxRuntime backed by the microsandbox driver.
func NewRuntimeServer(drv *runtime.Driver) *RuntimeServer {
	return &RuntimeServer{
		driver:      drv,
		volumeNames: make(map[string]string),
	}
}

// RegisterRuntime registers SandboxRuntime on s.
func RegisterRuntime(s *grpc.Server, rt *RuntimeServer) {
	cellarv1.RegisterSandboxRuntimeServer(s, rt)
}

func (r *RuntimeServer) Logs(req *cellarv1.SandboxLogsRequest, stream cellarv1.SandboxRuntime_LogsServer) error {
	if r.driver == nil {
		return status.Error(codes.Unavailable, "runtime not ready")
	}
	if req.SandboxId == "" {
		return status.Error(codes.InvalidArgument, "sandbox_id required")
	}
	var sources []string
	if req.Sources != "" {
		for _, s := range strings.Split(req.Sources, ",") {
			if t := strings.TrimSpace(s); t != "" {
				sources = append(sources, t)
			}
		}
	}
	return r.driver.FollowLogs(stream.Context(), req.SandboxId, runtime.LogFollowOptions{
		Follow:     req.Follow,
		Sources:    sources,
		FromCursor: req.LastEventId,
	}, func(e runtime.LogEntry) error {
		return stream.Send(&cellarv1.SandboxLogsChunk{
			Id:         e.ID,
			Source:     e.Source,
			TsUnixNano: e.Timestamp.UnixNano(),
			Text:       e.Text,
		})
	})
}

func (r *RuntimeServer) AgentRelay(stream cellarv1.SandboxRuntime_AgentRelayServer) error {
	if r.driver == nil {
		return status.Error(codes.Unavailable, "runtime not ready")
	}
	sandboxID := sandboxIDFromContext(stream.Context())
	if sandboxID == "" {
		return status.Error(codes.InvalidArgument, "sandbox-id metadata required")
	}
	sockPath, err := r.driver.AgentSocketPath(sandboxID)
	if err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		return status.Error(codes.Unavailable, err.Error())
	}
	defer conn.Close()

	errCh := make(chan error, 2)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := conn.Read(buf)
			if n > 0 {
				if serr := stream.Send(&cellarv1.AgentRelayChunk{Data: append([]byte(nil), buf[:n]...)}); serr != nil {
					errCh <- serr
					return
				}
			}
			if rerr != nil {
				if rerr != io.EOF {
					errCh <- rerr
				} else {
					errCh <- nil
				}
				return
			}
		}
	}()
	go func() {
		for {
			msg, rerr := stream.Recv()
			if rerr == io.EOF {
				errCh <- nil
				return
			}
			if rerr != nil {
				errCh <- rerr
				return
			}
			if len(msg.GetData()) == 0 {
				continue
			}
			if _, werr := conn.Write(msg.GetData()); werr != nil {
				errCh <- werr
				return
			}
		}
	}()
	err = <-errCh
	_ = conn.Close()
	<-errCh
	if err != nil && err != io.EOF {
		return err
	}
	return nil
}

func sandboxIDFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(SandboxIDMetadataKey)
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[0])
}

func (r *RuntimeServer) localVolumeName(volumeID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name, ok := r.volumeNames[volumeID]; ok && name != "" {
		return name
	}
	return volumeID
}

func (r *RuntimeServer) rememberVolume(volumeID, name string) {
	if volumeID == "" {
		return
	}
	if name == "" {
		name = volumeID
	}
	r.mu.Lock()
	r.volumeNames[volumeID] = name
	r.mu.Unlock()
}

func (r *RuntimeServer) forgetVolume(volumeID string) {
	r.mu.Lock()
	delete(r.volumeNames, volumeID)
	r.mu.Unlock()
}

func (r *RuntimeServer) volumeFS(ctx context.Context, volumeID string) (*msb.VolumeFs, error) {
	if r.driver == nil {
		return nil, status.Error(codes.Unavailable, "runtime not ready")
	}
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "volume_id required")
	}
	fs, err := r.driver.VolumeFS(ctx, r.localVolumeName(volumeID))
	if err != nil {
		return nil, MapFsErr(err)
	}
	return fs, nil
}

func (r *RuntimeServer) EnsureLocalVolume(ctx context.Context, req *cellarv1.EnsureLocalVolumeRequest) (*cellarv1.EnsureLocalVolumeResponse, error) {
	if r.driver == nil {
		return nil, status.Error(codes.Unavailable, "runtime not ready")
	}
	name := req.GetName()
	if name == "" {
		name = req.GetVolumeId()
	}
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name or volume_id required")
	}
	var cap *uint32
	if req.CapacityGib != nil {
		v := req.GetCapacityGib()
		cap = &v
	}
	if err := r.driver.EnsureVolume(ctx, name, cap); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	r.rememberVolume(req.GetVolumeId(), name)
	return &cellarv1.EnsureLocalVolumeResponse{}, nil
}

func (r *RuntimeServer) DeleteLocalVolume(ctx context.Context, req *cellarv1.DeleteLocalVolumeRequest) (*cellarv1.DeleteLocalVolumeResponse, error) {
	if r.driver == nil {
		return nil, status.Error(codes.Unavailable, "runtime not ready")
	}
	name := req.GetName()
	if name == "" {
		name = r.localVolumeName(req.GetVolumeId())
	}
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name or volume_id required")
	}
	if err := r.driver.DeleteVolume(ctx, name); err != nil {
		return nil, MapFsErr(err)
	}
	r.forgetVolume(req.GetVolumeId())
	return &cellarv1.DeleteLocalVolumeResponse{}, nil
}

func (r *RuntimeServer) VolumeFsRead(req *cellarv1.VolumeFsReadRequest, stream cellarv1.SandboxRuntime_VolumeFsReadServer) error {
	fs, err := r.volumeFS(stream.Context(), req.GetVolumeId())
	if err != nil {
		return err
	}
	if req.GetPath() == "" {
		return status.Error(codes.InvalidArgument, "path required")
	}
	data, err := fs.Read(stream.Context(), req.GetPath())
	if err != nil {
		return MapFsErr(err)
	}
	const chunk = 32 * 1024
	for i := 0; i < len(data); i += chunk {
		end := i + chunk
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&cellarv1.FsChunk{Data: data[i:end]}); err != nil {
			return err
		}
	}
	return nil
}

func (r *RuntimeServer) VolumeFsWrite(stream cellarv1.SandboxRuntime_VolumeFsWriteServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be start")
	}
	fs, err := r.volumeFS(stream.Context(), start.GetVolumeId())
	if err != nil {
		return err
	}
	if start.GetPath() == "" {
		return status.Error(codes.InvalidArgument, "path required")
	}
	var buf []byte
	if d := first.GetData(); len(d) > 0 {
		buf = append(buf, d...)
	}
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if d := msg.GetData(); len(d) > 0 {
			buf = append(buf, d...)
		}
	}
	if err := fs.Write(stream.Context(), start.GetPath(), buf); err != nil {
		return MapFsErr(err)
	}
	return stream.SendAndClose(&cellarv1.VolumeFsWriteResponse{})
}

func (r *RuntimeServer) VolumeFsStat(ctx context.Context, req *cellarv1.VolumeFsStatRequest) (*cellarv1.VolumeFsStatResponse, error) {
	fs, err := r.volumeFS(ctx, req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	meta, err := volumeStat(fs, req.GetPath())
	if err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.VolumeFsStatResponse{Metadata: meta}, nil
}

func (r *RuntimeServer) VolumeFsList(ctx context.Context, req *cellarv1.VolumeFsListRequest) (*cellarv1.VolumeFsListResponse, error) {
	fs, err := r.volumeFS(ctx, req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	entries, err := volumeList(fs, req.GetPath())
	if err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.VolumeFsListResponse{Entries: entries}, nil
}

func (r *RuntimeServer) VolumeFsExists(ctx context.Context, req *cellarv1.VolumeFsExistsRequest) (*cellarv1.VolumeFsExistsResponse, error) {
	fs, err := r.volumeFS(ctx, req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	ok, err := fs.Exists(ctx, req.GetPath())
	if err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.VolumeFsExistsResponse{Exists: ok}, nil
}

func (r *RuntimeServer) VolumeFsMkdir(ctx context.Context, req *cellarv1.VolumeFsMkdirRequest) (*cellarv1.VolumeFsMkdirResponse, error) {
	fs, err := r.volumeFS(ctx, req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	if err := fs.Mkdir(ctx, req.GetPath()); err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.VolumeFsMkdirResponse{}, nil
}

func (r *RuntimeServer) VolumeFsRemove(ctx context.Context, req *cellarv1.VolumeFsRemoveRequest) (*cellarv1.VolumeFsRemoveResponse, error) {
	fs, err := r.volumeFS(ctx, req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	var remErr error
	if req.GetRecursive() {
		remErr = fs.RemoveAll(ctx, req.GetPath())
	} else {
		remErr = fs.Remove(ctx, req.GetPath())
	}
	if remErr != nil {
		return nil, MapFsErr(remErr)
	}
	return &cellarv1.VolumeFsRemoveResponse{}, nil
}

func (r *RuntimeServer) VolumeFsCopy(ctx context.Context, req *cellarv1.VolumeFsCopyRequest) (*cellarv1.VolumeFsCopyResponse, error) {
	fs, err := r.volumeFS(ctx, req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	if err := volumeCopy(ctx, fs, req.GetFrom(), req.GetTo()); err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.VolumeFsCopyResponse{}, nil
}

func (r *RuntimeServer) VolumeFsRename(ctx context.Context, req *cellarv1.VolumeFsRenameRequest) (*cellarv1.VolumeFsRenameResponse, error) {
	fs, err := r.volumeFS(ctx, req.GetVolumeId())
	if err != nil {
		return nil, err
	}
	if err := volumeRename(fs, req.GetFrom(), req.GetTo()); err != nil {
		return nil, MapFsErr(err)
	}
	return &cellarv1.VolumeFsRenameResponse{}, nil
}

func volumeStat(fs *msb.VolumeFs, rel string) (*cellarv1.FsMetadata, error) {
	root := fs.Root()
	if root == "" {
		return nil, status.Error(codes.FailedPrecondition, "volume root unavailable")
	}
	abs := filepath.Join(root, rel)
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	kind := "file"
	if fi.IsDir() {
		kind = "directory"
	}
	mod := fi.ModTime().UnixNano()
	return &cellarv1.FsMetadata{
		Kind:             kind,
		Size:             fi.Size(),
		Mode:             uint32(fi.Mode().Perm()),
		ModifiedUnixNano: mod,
		Path:             rel,
	}, nil
}

func volumeList(fs *msb.VolumeFs, rel string) ([]*cellarv1.FsEntry, error) {
	root := fs.Root()
	if root == "" {
		return nil, status.Error(codes.FailedPrecondition, "volume root unavailable")
	}
	abs := filepath.Join(root, rel)
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]*cellarv1.FsEntry, 0, len(entries))
	for _, e := range entries {
		kind := "file"
		if e.IsDir() {
			kind = "directory"
		}
		var size int64
		var mod int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
			mod = info.ModTime().UnixNano()
		}
		out = append(out, &cellarv1.FsEntry{
			Path:             filepath.Join(rel, e.Name()),
			Kind:             kind,
			Size:             size,
			ModifiedUnixNano: mod,
		})
	}
	return out, nil
}

func volumeCopy(ctx context.Context, fs *msb.VolumeFs, from, to string) error {
	data, err := fs.Read(ctx, from)
	if err != nil {
		return err
	}
	return fs.Write(ctx, to, data)
}

func volumeRename(fs *msb.VolumeFs, from, to string) error {
	root := fs.Root()
	if root == "" {
		return status.Error(codes.FailedPrecondition, "volume root unavailable")
	}
	return os.Rename(filepath.Join(root, from), filepath.Join(root, to))
}

// MapFsErr maps filesystem errors to gRPC status errors.
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
		strings.Contains(lower, "volume_id"),
		strings.Contains(lower, "escapes"):
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
		strings.Contains(lower, "directory not empty"):
		return status.Error(codes.FailedPrecondition, msg)
	default:
		return status.Error(codes.Internal, msg)
	}
}
