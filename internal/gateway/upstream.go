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
)

// Upstream is the SandboxAPI surface the gateway proxies to.
type Upstream interface {
	Create(ctx context.Context, apiKey string, req *cellarv1.SandboxCreateRequest) (*cellarv1.Sandbox, error)
	Stop(ctx context.Context, apiKey, id string) (*cellarv1.Sandbox, error)
	Remove(ctx context.Context, apiKey, id string) error
	Get(ctx context.Context, apiKey, id string) (*cellarv1.Sandbox, error)
	List(ctx context.Context, apiKey string) ([]*cellarv1.Sandbox, error)
	UpdateNetwork(ctx context.Context, apiKey string, req *cellarv1.SandboxUpdateNetworkRequest) (*cellarv1.Sandbox, error)
	Logs(ctx context.Context, apiKey string, req *cellarv1.SandboxLogsRequest) (LogsStream, error)
	Exec(ctx context.Context, apiKey, sandboxID string, command []string) (*ExecResult, error)
	StartJob(ctx context.Context, apiKey, sandboxID string, command []string) (jobID string, err error)
	ListJobs(ctx context.Context, apiKey, sandboxID string) ([]*cellarv1.JobInfo, error)
	GetJob(ctx context.Context, apiKey, sandboxID, jobID string) (*cellarv1.JobInfo, error)
	StopJob(ctx context.Context, apiKey, sandboxID, jobID string, timeoutSec int32) error
	JobLogs(ctx context.Context, apiKey string, req *cellarv1.JobLogsRequest) (LogsStream, error)
	// Ready probes whether SandboxAPI is reachable (Unauthenticated counts as ready).
	Ready(ctx context.Context) error
}

// LogsStream yields log chunks until EOF.
type LogsStream interface {
	Recv() (*cellarv1.SandboxLogsChunk, error)
	Close() error
}

// ExecResult is the outcome of a non-interactive exec.
type ExecResult struct {
	Stdout   []byte `json:"stdout"`
	Stderr   []byte `json:"stderr"`
	ExitCode int32  `json:"exitCode"`
	Error    string `json:"error,omitempty"`
}

// Resolver discovers manager addresses and the cluster CA.
type Resolver interface {
	Resolve() (addrs []string, caPEM []byte, err error)
}

// DataDirResolver loads node identity from a cellard data directory.
type DataDirResolver struct {
	DataDir   string
	Overrides []string
}

// Resolve returns manager gRPC addresses and the cluster CA PEM.
// When Overrides is set, those addresses are used; otherwise managers use
// AdvertiseAddr and workers use ManagerAddr. Identity is reloaded each call
// so role changes take effect without restarting the gateway.
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
	var addr string
	switch state.Role {
	case node.RoleManager:
		addr = state.AdvertiseAddr
		if addr == "" {
			addr = state.ListenAddr
		}
	default:
		addr = state.ManagerAddr
		if addr == "" {
			addr = state.AdvertiseAddr
		}
	}
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, nil, fmt.Errorf("no manager address in %s (set --upstreams or join/init cellard)", r.DataDir)
	}
	return []string{addr}, append([]byte(nil), mat.CACert...), nil
}

// GRPCUpstream dials SandboxAPI over TLS with the cluster CA.
type GRPCUpstream struct {
	Resolver Resolver

	mu       sync.Mutex
	lastOK   string
	badUntil map[string]time.Time
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
		// Cap each failover attempt so a dead manager does not burn the whole deadline.
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

func (u *GRPCUpstream) Create(ctx context.Context, apiKey string, req *cellarv1.SandboxCreateRequest) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.Create(withAPIKey(ctx, apiKey), req)
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

func (u *GRPCUpstream) List(ctx context.Context, apiKey string) ([]*cellarv1.Sandbox, error) {
	var out []*cellarv1.Sandbox
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.List(withAPIKey(ctx, apiKey), &cellarv1.SandboxListRequest{})
		if err != nil {
			return err
		}
		out = resp.Sandboxes
		return nil
	})
	return out, err
}

func (u *GRPCUpstream) UpdateNetwork(ctx context.Context, apiKey string, req *cellarv1.SandboxUpdateNetworkRequest) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.UpdateNetwork(withAPIKey(ctx, apiKey), req)
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
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

func (u *GRPCUpstream) Exec(ctx context.Context, apiKey, sandboxID string, command []string) (*ExecResult, error) {
	var res *ExecResult
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		stream, err := api.Exec(withAPIKey(ctx, apiKey))
		if err != nil {
			return err
		}
		if err := stream.Send(&cellarv1.SandboxExecMessage{
			Payload: &cellarv1.SandboxExecMessage_Start{Start: &cellarv1.SandboxExecStart{
				SandboxId: sandboxID,
				Command:   command,
			}},
		}); err != nil {
			return err
		}
		_ = stream.CloseSend()
		out := &ExecResult{}
		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			switch p := msg.Payload.(type) {
			case *cellarv1.SandboxExecMessage_Stdout:
				out.Stdout = append(out.Stdout, p.Stdout...)
			case *cellarv1.SandboxExecMessage_Stderr:
				out.Stderr = append(out.Stderr, p.Stderr...)
			case *cellarv1.SandboxExecMessage_Exit:
				out.ExitCode = p.Exit.ExitCode
				out.Error = p.Exit.Error
				res = out
				return nil
			}
		}
		res = out
		return nil
	})
	return res, err
}

func (u *GRPCUpstream) StartJob(ctx context.Context, apiKey, sandboxID string, command []string) (string, error) {
	var jobID string
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.StartJob(withAPIKey(ctx, apiKey), &cellarv1.StartJobRequest{
			SandboxId: sandboxID,
			Command:   command,
		})
		if err != nil {
			return err
		}
		jobID = resp.JobId
		return nil
	})
	return jobID, err
}

func (u *GRPCUpstream) ListJobs(ctx context.Context, apiKey, sandboxID string) ([]*cellarv1.JobInfo, error) {
	var jobs []*cellarv1.JobInfo
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.ListJobs(withAPIKey(ctx, apiKey), &cellarv1.ListJobsRequest{SandboxId: sandboxID})
		if err != nil {
			return err
		}
		jobs = resp.Jobs
		return nil
	})
	return jobs, err
}

func (u *GRPCUpstream) GetJob(ctx context.Context, apiKey, sandboxID, jobID string) (*cellarv1.JobInfo, error) {
	var job *cellarv1.JobInfo
	err := u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.GetJob(withAPIKey(ctx, apiKey), &cellarv1.GetJobRequest{
			SandboxId: sandboxID,
			JobId:     jobID,
		})
		if err != nil {
			return err
		}
		job = resp.Job
		return nil
	})
	return job, err
}

func (u *GRPCUpstream) StopJob(ctx context.Context, apiKey, sandboxID, jobID string, timeoutSec int32) error {
	return u.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		_, err := api.StopJob(withAPIKey(ctx, apiKey), &cellarv1.StopJobRequest{
			SandboxId:  sandboxID,
			JobId:      jobID,
			TimeoutSec: timeoutSec,
		})
		return err
	})
}

func (u *GRPCUpstream) JobLogs(ctx context.Context, apiKey string, req *cellarv1.JobLogsRequest) (LogsStream, error) {
	addrs, caPEM, err := u.Resolver.Resolve()
	if err != nil {
		return nil, err
	}
	tlsCfg, err := grpcapi.ClientTLSFromPEMs(nil, nil, caPEM, grpcapi.TLSServerName)
	if err != nil {
		return nil, err
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
		ctx2, cancel := context.WithCancel(ctx)
		stream, err := cellarv1.NewSandboxAPIClient(conn).JobLogs(withAPIKey(ctx2, apiKey), req)
		if err != nil {
			cancel()
			_ = conn.Close()
			lastErr = err
			continue
		}
		return &grpcLogsStream{stream: stream, conn: conn, cancel: cancel}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no upstream managers reachable")
	}
	return nil, lastErr
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
