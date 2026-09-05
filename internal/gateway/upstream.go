package gateway

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/grpcapi"
	"github.com/prodioslabs/cellar/internal/identity"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/paths"
)

// Upstream is the SandboxAPI + runtime surface the gateway proxies to.
type Upstream interface {
	Create(ctx context.Context, apiKey string, specJSON []byte, start bool) (*cellarv1.Sandbox, error)
	Start(ctx context.Context, apiKey, id string) (*cellarv1.Sandbox, error)
	Stop(ctx context.Context, apiKey, id string) (*cellarv1.Sandbox, error)
	Remove(ctx context.Context, apiKey, id string) error
	Get(ctx context.Context, apiKey, id string) (*cellarv1.Sandbox, error)
	GetByName(ctx context.Context, apiKey, name string) (*cellarv1.Sandbox, error)
	List(ctx context.Context, apiKey, cursor string, limit uint32, labelsJSON string) ([]*cellarv1.Sandbox, string, error)

	Logs(ctx context.Context, apiKey string, req *cellarv1.SandboxLogsRequest) (LogsStream, error)
	AgentRelay(ctx context.Context, apiKey, sandboxID string) (AgentRelayStream, error)

	CreateVolume(ctx context.Context, apiKey string, req *cellarv1.VolumeCreateRequest) (*cellarv1.Volume, error)
	ListVolumes(ctx context.Context, apiKey string) ([]*cellarv1.Volume, error)
	GetVolume(ctx context.Context, apiKey, id string) (*cellarv1.Volume, error)
	GetDefaultVolume(ctx context.Context, apiKey string) (*cellarv1.Volume, error)
	DeleteVolume(ctx context.Context, apiKey, id string) (string, error)

	VolumeFsRead(ctx context.Context, apiKey, volumeID, path string) (FsStream, error)
	VolumeFsWrite(ctx context.Context, apiKey, volumeID, path string, r io.Reader) error
	VolumeFsStat(ctx context.Context, apiKey, volumeID, path string) (*cellarv1.FsMetadata, error)
	VolumeFsList(ctx context.Context, apiKey, volumeID, path string) ([]*cellarv1.FsEntry, error)
	VolumeFsExists(ctx context.Context, apiKey, volumeID, path string) (bool, error)
	VolumeFsMkdir(ctx context.Context, apiKey, volumeID, path string) error
	VolumeFsRemove(ctx context.Context, apiKey, volumeID, path string, recursive bool) error
	VolumeFsCopy(ctx context.Context, apiKey, volumeID, from, to string) error
	VolumeFsRename(ctx context.Context, apiKey, volumeID, from, to string) error

	Ready(ctx context.Context) error
	ClusterID() string
}

// LogsStream yields log chunks until EOF.
type LogsStream interface {
	Recv() (*cellarv1.SandboxLogsChunk, error)
	Close() error
}

// FsStream yields filesystem content chunks until EOF.
type FsStream interface {
	Recv() (*cellarv1.FsChunk, error)
	Close() error
}

// AgentRelayStream is a bidirectional byte pipe to the guest agent.
type AgentRelayStream interface {
	Send(data []byte) error
	Recv() ([]byte, error)
	Close() error
}

// Resolver discovers manager addresses and the cluster CA.
type Resolver interface {
	Resolve() (addrs []string, caPEM []byte, err error)
}

// IdentityProvider loads node mTLS material and cluster ID.
type IdentityProvider interface {
	Identity() (cert, key, ca []byte, clusterID string, err error)
}

// RuntimeAddrResolver looks up a node's SandboxRuntime address.
type RuntimeAddrResolver interface {
	RuntimeAddr(ctx context.Context, nodeID string) (string, error)
}

// DataDirResolver loads node identity from a cellard data directory.
type DataDirResolver struct {
	DataDir    string
	SocketPath string
	Overrides  []string
}

// Resolve returns manager gRPC addresses and the cluster CA PEM.
func (r *DataDirResolver) Resolve() ([]string, []byte, error) {
	store := identity.NewStore(r.DataDir)
	if err := store.Load(); err != nil {
		return nil, nil, fmt.Errorf("load identity: %w", err)
	}
	mat := store.Material()
	if mat == nil || len(mat.CACert) == 0 {
		return nil, nil, fmt.Errorf("no cluster CA in %s (is cellard initialized?)", r.DataDir)
	}
	if len(r.Overrides) > 0 {
		return append([]string(nil), r.Overrides...), append([]byte(nil), mat.CACert...), nil
	}
	state := store.State()
	var addrs []string
	switch state.Role {
	case node.RoleManager:
		addr := state.AdvertiseAddr
		if addr == "" {
			addr = state.ListenAddr
		}
		addrs = grpcapi.MergeManagerAddrs(strings.TrimSpace(addr), state.ManagerAddrs)
	default:
		addrs = grpcapi.MergeManagerAddrs(strings.TrimSpace(state.ManagerAddr), state.ManagerAddrs, []string{strings.TrimSpace(state.AdvertiseAddr)})
	}
	if len(addrs) == 0 {
		return nil, nil, fmt.Errorf("no manager address in %s (set --upstreams or join/init cellard)", r.DataDir)
	}
	return addrs, append([]byte(nil), mat.CACert...), nil
}

// Identity returns node cert/key/CA and cluster ID from the data directory.
func (r *DataDirResolver) Identity() (cert, key, ca []byte, clusterID string, err error) {
	store := identity.NewStore(r.DataDir)
	if err := store.Load(); err != nil {
		return nil, nil, nil, "", fmt.Errorf("load identity: %w", err)
	}
	mat := store.Material()
	if mat == nil {
		return nil, nil, nil, "", fmt.Errorf("no identity in %s", r.DataDir)
	}
	state := store.State()
	cid := mat.ClusterID
	if cid == "" {
		cid = state.ClusterID
	}
	return append([]byte(nil), mat.Certificate...),
		append([]byte(nil), mat.PrivateKey...),
		append([]byte(nil), mat.CACert...),
		cid, nil
}

// RuntimeAddr resolves a node's SandboxRuntime address via local Control.
func (r *DataDirResolver) RuntimeAddr(ctx context.Context, nodeID string) (string, error) {
	if nodeID == "" {
		return "", fmt.Errorf("node_id required")
	}
	sock := r.SocketPath
	if sock == "" {
		sock = paths.DefaultSocket
	}
	conn, err := paths.DialLocal(sock)
	if err != nil {
		return "", fmt.Errorf("dial control socket: %w", err)
	}
	defer conn.Close()
	resp, err := cellarv1.NewControlClient(conn).NodeInspect(ctx, &cellarv1.NodeInspectRequest{NodeId: nodeID})
	if err != nil {
		return "", err
	}
	if resp.Node == nil {
		return "", fmt.Errorf("node %s not found", nodeID)
	}
	addr := strings.TrimSpace(resp.Node.RuntimeGrpcAddr)
	if addr == "" {
		return "", fmt.Errorf("runtime address for node %s unknown (waiting for heartbeat)", nodeID)
	}
	return addr, nil
}

// GRPCUpstream dials SandboxAPI over TLS with the cluster CA.
type GRPCUpstream struct {
	Resolver Resolver
	Identity IdentityProvider
	Runtime  RuntimeAddrResolver

	mu        sync.Mutex
	lastOK    string
	badUntil  map[string]time.Time
	clusterID string
}

func withAPIKey(ctx context.Context, apiKey string) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		"authorization", "Bearer "+apiKey,
		"x-api-key", apiKey,
	)
}

func normalizeGRPCAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "dns:///") || strings.Contains(addr, "://") {
		return addr
	}
	return "dns:///" + addr
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return true
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

func (u *GRPCUpstream) markBad(addr string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.badUntil == nil {
		u.badUntil = make(map[string]time.Time)
	}
	u.badUntil[addr] = time.Now().Add(5 * time.Second)
	if u.lastOK == addr {
		u.lastOK = ""
	}
}

func (u *GRPCUpstream) markOK(addr string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastOK = addr
	if u.badUntil != nil {
		delete(u.badUntil, addr)
	}
}

func (u *GRPCUpstream) pickOrder(addrs []string) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := time.Now()
	order := make([]string, 0, len(addrs))
	if u.lastOK != "" {
		for _, a := range addrs {
			if a == u.lastOK {
				if until, bad := u.badUntil[a]; !bad || now.After(until) {
					order = append(order, a)
				}
				break
			}
		}
	}
	for _, a := range addrs {
		if a == u.lastOK {
			continue
		}
		if until, bad := u.badUntil[a]; bad && now.Before(until) {
			continue
		}
		order = append(order, a)
	}
	if len(order) == 0 {
		order = append(order, addrs...)
	}
	return order
}

func (u *GRPCUpstream) withConn(ctx context.Context, fn func(ctx context.Context, api cellarv1.SandboxAPIClient) error) error {
	addrs, caPEM, err := u.Resolver.Resolve()
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return fmt.Errorf("no upstream managers")
	}
	tlsCfg, err := grpcapi.ClientTLSFromPEMs(nil, nil, caPEM, grpcapi.TLSServerName)
	if err != nil {
		return err
	}
	order := u.pickOrder(addrs)
	var lastErr error
	for i, addr := range order {
		attemptCtx := ctx
		var cancel context.CancelFunc
		if len(order) > 1 && i < len(order)-1 {
			attemptCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
		}
		conn, err := grpc.NewClient(
			normalizeGRPCAddr(addr),
			grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		)
		if err != nil {
			if cancel != nil {
				cancel()
			}
			u.markBad(addr)
			lastErr = err
			continue
		}
		err = fn(attemptCtx, cellarv1.NewSandboxAPIClient(conn))
		_ = conn.Close()
		if cancel != nil {
			cancel()
		}
		if err == nil {
			u.markOK(addr)
			return nil
		}
		lastErr = err
		if retryable(err) || (cancel != nil && attemptCtx.Err() != nil) {
			u.markBad(addr)
			continue
		}
		return err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no upstream managers reachable")
	}
	return lastErr
}

func (u *GRPCUpstream) ClusterID() string {
	u.mu.Lock()
	if u.clusterID != "" {
		id := u.clusterID
		u.mu.Unlock()
		return id
	}
	u.mu.Unlock()
	if u.Identity == nil {
		return ""
	}
	_, _, _, cid, err := u.Identity.Identity()
	if err != nil {
		return ""
	}
	u.mu.Lock()
	u.clusterID = cid
	u.mu.Unlock()
	return cid
}

func (u *GRPCUpstream) Create(ctx context.Context, apiKey string, specJSON []byte, start bool) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.Create(withAPIKey(ctx, apiKey), &cellarv1.SandboxCreateRequest{
			SpecJson: specJSON,
			Start:    start,
		})
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) Start(ctx context.Context, apiKey, id string) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.Start(withAPIKey(ctx, apiKey), &cellarv1.SandboxStartRequest{SandboxId: id})
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) Stop(ctx context.Context, apiKey, id string) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.Stop(withAPIKey(ctx, apiKey), &cellarv1.SandboxStopRequest{SandboxId: id})
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) Remove(ctx context.Context, apiKey, id string) error {
	return u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		_, err := api.Remove(withAPIKey(ctx, apiKey), &cellarv1.SandboxRemoveRequest{SandboxId: id})
		return err
	})
}

func (u *GRPCUpstream) Get(ctx context.Context, apiKey, id string) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.Get(withAPIKey(ctx, apiKey), &cellarv1.SandboxGetRequest{SandboxId: id})
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) GetByName(ctx context.Context, apiKey, name string) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.GetByName(withAPIKey(ctx, apiKey), &cellarv1.SandboxGetByNameRequest{Name: name})
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) List(ctx context.Context, apiKey, cursor string, limit uint32, labelsJSON string) ([]*cellarv1.Sandbox, string, error) {
	var out []*cellarv1.Sandbox
	var next string
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.List(withAPIKey(ctx, apiKey), &cellarv1.SandboxListRequest{
			Cursor:     cursor,
			Limit:      limit,
			LabelsJson: labelsJSON,
		})
		if err != nil {
			return err
		}
		out = resp.Sandboxes
		next = resp.NextCursor
		return nil
	})
	return out, next, err
}

type grpcLogsStream struct {
	stream interface {
		Recv() (*cellarv1.SandboxLogsChunk, error)
	}
	conn   *grpc.ClientConn
	cancel context.CancelFunc
}

func (s *grpcLogsStream) Recv() (*cellarv1.SandboxLogsChunk, error) {
	return s.stream.Recv()
}

func (s *grpcLogsStream) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (u *GRPCUpstream) Logs(ctx context.Context, apiKey string, req *cellarv1.SandboxLogsRequest) (LogsStream, error) {
	addrs, caPEM, err := u.Resolver.Resolve()
	if err != nil {
		return nil, err
	}
	tlsCfg, err := grpcapi.ClientTLSFromPEMs(nil, nil, caPEM, grpcapi.TLSServerName)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, addr := range u.pickOrder(addrs) {
		conn, err := grpc.NewClient(
			normalizeGRPCAddr(addr),
			grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		)
		if err != nil {
			u.markBad(addr)
			lastErr = err
			continue
		}
		ctx2, cancel := context.WithCancel(ctx)
		stream, err := cellarv1.NewSandboxAPIClient(conn).Logs(withAPIKey(ctx2, apiKey), req)
		if err != nil {
			cancel()
			_ = conn.Close()
			lastErr = err
			if retryable(err) {
				u.markBad(addr)
				continue
			}
			return nil, err
		}
		u.markOK(addr)
		return &grpcLogsStream{stream: stream, conn: conn, cancel: cancel}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no upstream managers reachable")
	}
	return nil, lastErr
}

func (u *GRPCUpstream) identityPEMs() (cert, key, ca []byte, err error) {
	if u.Identity == nil {
		return nil, nil, nil, fmt.Errorf("no identity provider")
	}
	cert, key, ca, _, err = u.Identity.Identity()
	return cert, key, ca, err
}

func (u *GRPCUpstream) runtimeAddr(ctx context.Context, nodeID string) (string, error) {
	if u.Runtime == nil {
		return "", fmt.Errorf("no runtime address resolver")
	}
	return u.Runtime.RuntimeAddr(ctx, nodeID)
}

func (u *GRPCUpstream) dialRuntime(ctx context.Context, nodeID string) (*grpc.ClientConn, error) {
	addr, err := u.runtimeAddr(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	cert, key, ca, err := u.identityPEMs()
	if err != nil {
		return nil, err
	}
	return grpcapi.DialRuntime(addr, cert, key, ca)
}

type grpcAgentRelayStream struct {
	stream cellarv1.SandboxRuntime_AgentRelayClient
	conn   *grpc.ClientConn
	cancel context.CancelFunc
}

func (s *grpcAgentRelayStream) Send(data []byte) error {
	return s.stream.Send(&cellarv1.AgentRelayChunk{Data: data})
}

func (s *grpcAgentRelayStream) Recv() ([]byte, error) {
	msg, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}
	return msg.GetData(), nil
}

func (s *grpcAgentRelayStream) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (u *GRPCUpstream) AgentRelay(ctx context.Context, apiKey, sandboxID string) (AgentRelayStream, error) {
	sb, err := u.Get(ctx, apiKey, sandboxID)
	if err != nil {
		return nil, err
	}
	if sb == nil || sb.NodeId == "" {
		return nil, status.Error(codes.FailedPrecondition, "sandbox has no owning node")
	}
	conn, err := u.dialRuntime(ctx, sb.NodeId)
	if err != nil {
		return nil, err
	}
	ctx2, cancel := context.WithCancel(ctx)
	ctx2 = metadata.NewOutgoingContext(ctx2, grpcapi.AgentRelayMetadata(sandboxID))
	stream, err := cellarv1.NewSandboxRuntimeClient(conn).AgentRelay(ctx2)
	if err != nil {
		cancel()
		_ = conn.Close()
		return nil, err
	}
	return &grpcAgentRelayStream{stream: stream, conn: conn, cancel: cancel}, nil
}

func (u *GRPCUpstream) CreateVolume(ctx context.Context, apiKey string, req *cellarv1.VolumeCreateRequest) (*cellarv1.Volume, error) {
	var out *cellarv1.Volume
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.CreateVolume(withAPIKey(ctx, apiKey), req)
		if err != nil {
			return err
		}
		out = resp.Volume
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) ListVolumes(ctx context.Context, apiKey string) ([]*cellarv1.Volume, error) {
	var out []*cellarv1.Volume
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.ListVolumes(withAPIKey(ctx, apiKey), &cellarv1.VolumeListRequest{})
		if err != nil {
			return err
		}
		out = resp.Volumes
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) GetVolume(ctx context.Context, apiKey, id string) (*cellarv1.Volume, error) {
	var out *cellarv1.Volume
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.GetVolume(withAPIKey(ctx, apiKey), &cellarv1.VolumeGetRequest{VolumeId: id})
		if err != nil {
			return err
		}
		out = resp.Volume
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) GetDefaultVolume(ctx context.Context, apiKey string) (*cellarv1.Volume, error) {
	var out *cellarv1.Volume
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.GetDefaultVolume(withAPIKey(ctx, apiKey), &cellarv1.VolumeGetDefaultRequest{})
		if err != nil {
			return err
		}
		out = resp.Volume
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) DeleteVolume(ctx context.Context, apiKey, id string) (string, error) {
	var msg string
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.DeleteVolume(withAPIKey(ctx, apiKey), &cellarv1.VolumeDeleteRequest{VolumeId: id})
		if err != nil {
			return err
		}
		msg = resp.GetMessage()
		return nil
	})
	return msg, err
}

func (u *GRPCUpstream) volumeNodeID(ctx context.Context, apiKey, volumeID string) (string, error) {
	vol, err := u.GetVolume(ctx, apiKey, volumeID)
	if err != nil {
		return "", err
	}
	if vol == nil || vol.NodeId == "" {
		return "", status.Error(codes.FailedPrecondition, "volume has no owning node")
	}
	return vol.NodeId, nil
}

type grpcFsStream struct {
	stream interface {
		Recv() (*cellarv1.FsChunk, error)
	}
	conn   *grpc.ClientConn
	cancel context.CancelFunc
}

func (s *grpcFsStream) Recv() (*cellarv1.FsChunk, error) {
	return s.stream.Recv()
}

func (s *grpcFsStream) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (u *GRPCUpstream) VolumeFsRead(ctx context.Context, apiKey, volumeID, path string) (FsStream, error) {
	nodeID, err := u.volumeNodeID(ctx, apiKey, volumeID)
	if err != nil {
		return nil, err
	}
	conn, err := u.dialRuntime(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	ctx2, cancel := context.WithCancel(ctx)
	stream, err := cellarv1.NewSandboxRuntimeClient(conn).VolumeFsRead(ctx2, &cellarv1.VolumeFsReadRequest{
		VolumeId: volumeID,
		Path:     path,
	})
	if err != nil {
		cancel()
		_ = conn.Close()
		return nil, err
	}
	return &grpcFsStream{stream: stream, conn: conn, cancel: cancel}, nil
}

func (u *GRPCUpstream) VolumeFsWrite(ctx context.Context, apiKey, volumeID, path string, r io.Reader) error {
	nodeID, err := u.volumeNodeID(ctx, apiKey, volumeID)
	if err != nil {
		return err
	}
	conn, err := u.dialRuntime(ctx, nodeID)
	if err != nil {
		return err
	}
	defer conn.Close()
	stream, err := cellarv1.NewSandboxRuntimeClient(conn).VolumeFsWrite(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&cellarv1.VolumeFsWriteMessage{
		Payload: &cellarv1.VolumeFsWriteMessage_Start{Start: &cellarv1.VolumeFsWriteStart{
			VolumeId: volumeID,
			Path:     path,
		}},
	}); err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if err := stream.Send(&cellarv1.VolumeFsWriteMessage{
				Payload: &cellarv1.VolumeFsWriteMessage_Data{Data: append([]byte(nil), buf[:n]...)},
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
}

func (u *GRPCUpstream) withRuntime(ctx context.Context, apiKey, volumeID string, fn func(ctx context.Context, rt cellarv1.SandboxRuntimeClient) error) error {
	nodeID, err := u.volumeNodeID(ctx, apiKey, volumeID)
	if err != nil {
		return err
	}
	conn, err := u.dialRuntime(ctx, nodeID)
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(ctx, cellarv1.NewSandboxRuntimeClient(conn))
}

func (u *GRPCUpstream) VolumeFsStat(ctx context.Context, apiKey, volumeID, path string) (*cellarv1.FsMetadata, error) {
	var out *cellarv1.FsMetadata
	err := u.withRuntime(ctx, apiKey, volumeID, func(ctx context.Context, rt cellarv1.SandboxRuntimeClient) error {
		resp, err := rt.VolumeFsStat(ctx, &cellarv1.VolumeFsStatRequest{VolumeId: volumeID, Path: path})
		if err != nil {
			return err
		}
		out = resp.Metadata
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) VolumeFsList(ctx context.Context, apiKey, volumeID, path string) ([]*cellarv1.FsEntry, error) {
	var out []*cellarv1.FsEntry
	err := u.withRuntime(ctx, apiKey, volumeID, func(ctx context.Context, rt cellarv1.SandboxRuntimeClient) error {
		resp, err := rt.VolumeFsList(ctx, &cellarv1.VolumeFsListRequest{VolumeId: volumeID, Path: path})
		if err != nil {
			return err
		}
		out = resp.Entries
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) VolumeFsExists(ctx context.Context, apiKey, volumeID, path string) (bool, error) {
	var exists bool
	err := u.withRuntime(ctx, apiKey, volumeID, func(ctx context.Context, rt cellarv1.SandboxRuntimeClient) error {
		resp, err := rt.VolumeFsExists(ctx, &cellarv1.VolumeFsExistsRequest{VolumeId: volumeID, Path: path})
		if err != nil {
			return err
		}
		exists = resp.Exists
		return nil
	})
	return exists, err
}

func (u *GRPCUpstream) VolumeFsMkdir(ctx context.Context, apiKey, volumeID, path string) error {
	return u.withRuntime(ctx, apiKey, volumeID, func(ctx context.Context, rt cellarv1.SandboxRuntimeClient) error {
		_, err := rt.VolumeFsMkdir(ctx, &cellarv1.VolumeFsMkdirRequest{VolumeId: volumeID, Path: path})
		return err
	})
}

func (u *GRPCUpstream) VolumeFsRemove(ctx context.Context, apiKey, volumeID, path string, recursive bool) error {
	return u.withRuntime(ctx, apiKey, volumeID, func(ctx context.Context, rt cellarv1.SandboxRuntimeClient) error {
		_, err := rt.VolumeFsRemove(ctx, &cellarv1.VolumeFsRemoveRequest{
			VolumeId:  volumeID,
			Path:      path,
			Recursive: recursive,
		})
		return err
	})
}

func (u *GRPCUpstream) VolumeFsCopy(ctx context.Context, apiKey, volumeID, from, to string) error {
	return u.withRuntime(ctx, apiKey, volumeID, func(ctx context.Context, rt cellarv1.SandboxRuntimeClient) error {
		_, err := rt.VolumeFsCopy(ctx, &cellarv1.VolumeFsCopyRequest{VolumeId: volumeID, From: from, To: to})
		return err
	})
}

func (u *GRPCUpstream) VolumeFsRename(ctx context.Context, apiKey, volumeID, from, to string) error {
	return u.withRuntime(ctx, apiKey, volumeID, func(ctx context.Context, rt cellarv1.SandboxRuntimeClient) error {
		_, err := rt.VolumeFsRename(ctx, &cellarv1.VolumeFsRenameRequest{VolumeId: volumeID, From: from, To: to})
		return err
	})
}

// Ready dials SandboxAPI without an API key. Unauthenticated means the service
// is present; Unimplemented or transport errors mean not ready.
func (u *GRPCUpstream) Ready(ctx context.Context) error {
	addrs, caPEM, err := u.Resolver.Resolve()
	if err != nil {
		return err
	}
	tlsCfg, err := grpcapi.ClientTLSFromPEMs(nil, nil, caPEM, grpcapi.TLSServerName)
	if err != nil {
		return err
	}
	var lastErr error
	for _, addr := range addrs {
		conn, err := grpc.NewClient(
			normalizeGRPCAddr(addr),
			grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		)
		if err != nil {
			lastErr = err
			continue
		}
		_, err = cellarv1.NewSandboxAPIClient(conn).List(ctx, &cellarv1.SandboxListRequest{})
		_ = conn.Close()
		if err == nil {
			return nil
		}
		st, ok := status.FromError(err)
		if !ok {
			lastErr = err
			continue
		}
		switch st.Code() {
		case codes.Unauthenticated, codes.PermissionDenied:
			return nil
		case codes.Unimplemented:
			lastErr = fmt.Errorf("SandboxAPI not available on %s", addr)
		default:
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no upstream managers")
	}
	return lastErr
}
