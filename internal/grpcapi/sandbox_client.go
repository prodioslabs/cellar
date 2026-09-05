package grpcapi

import (
	"context"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

// SandboxIDMetadataKey is the gRPC metadata key for AgentRelay sandbox id.
const SandboxIDMetadataKey = "sandbox-id"

// DialMTLS dials a manager with client certificates (control plane).
func DialMTLS(addr string, certPEM, keyPEM, caPEM []byte) (*grpc.ClientConn, error) {
	tlsCfg, err := ClientTLSFromPEMs(certPEM, keyPEM, caPEM, TLSServerName)
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(normalizeAddr(addr), grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
}

// DialRuntime dials a node's SandboxRuntime service.
func DialRuntime(addr string, certPEM, keyPEM, caPEM []byte) (*grpc.ClientConn, error) {
	tlsCfg, err := ClientTLSFromPEMs(certPEM, keyPEM, caPEM, TLSRuntimeServerName)
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(normalizeAddr(addr), grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
}

// SandboxCreateRemote creates a sandbox via SandboxControl.
func SandboxCreateRemote(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte, req *cellarv1.SandboxCreateRequest) (*cellarv1.SandboxCreateResponse, error) {
	conn, err := DialMTLS(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).Create(ctx, req)
}

// SandboxStartRemote starts a sandbox via SandboxControl.
func SandboxStartRemote(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte, id string) (*cellarv1.SandboxStartResponse, error) {
	conn, err := DialMTLS(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).Start(ctx, &cellarv1.SandboxStartRequest{SandboxId: id})
}

// SandboxStopRemote stops a sandbox.
func SandboxStopRemote(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte, id string) (*cellarv1.SandboxStopResponse, error) {
	conn, err := DialMTLS(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).Stop(ctx, &cellarv1.SandboxStopRequest{SandboxId: id})
}

// SandboxRemoveRemote removes a sandbox.
func SandboxRemoveRemote(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte, id string) error {
	conn, err := DialMTLS(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = cellarv1.NewSandboxControlClient(conn).Remove(ctx, &cellarv1.SandboxRemoveRequest{SandboxId: id})
	return err
}

// SandboxGetRemote gets a sandbox.
func SandboxGetRemote(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte, id string) (*cellarv1.SandboxGetResponse, error) {
	conn, err := DialMTLS(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).Get(ctx, &cellarv1.SandboxGetRequest{SandboxId: id})
}

// SandboxGetByNameRemote gets a sandbox by name.
func SandboxGetByNameRemote(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte, name string) (*cellarv1.SandboxGetResponse, error) {
	conn, err := DialMTLS(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).GetByName(ctx, &cellarv1.SandboxGetByNameRequest{Name: name})
}

// SandboxListRemote lists sandboxes.
func SandboxListRemote(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte) (*cellarv1.SandboxListResponse, error) {
	conn, err := DialMTLS(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).List(ctx, &cellarv1.SandboxListRequest{})
}

// RuntimeHeartbeatRemote sends a heartbeat and receives assignments.
func RuntimeHeartbeatRemote(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte, req *cellarv1.RuntimeHeartbeatRequest) (*cellarv1.RuntimeHeartbeatResponse, error) {
	conn, err := DialMTLS(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewRuntimeAgentClient(conn).Heartbeat(ctx, req)
}

// UpdateSandboxStatusRemote reports status.
func UpdateSandboxStatusRemote(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte, req *cellarv1.UpdateSandboxStatusRequest) error {
	conn, err := DialMTLS(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = cellarv1.NewRuntimeAgentClient(conn).UpdateSandboxStatus(ctx, req)
	return err
}

// StreamRemoteLogs forwards remote SandboxRuntime.Logs chunks via send.
func StreamRemoteLogs(ctx context.Context, addr string, certPEM, keyPEM, caPEM []byte, req *cellarv1.SandboxLogsRequest, send func(*cellarv1.SandboxLogsChunk) error) error {
	conn, err := DialRuntime(addr, certPEM, keyPEM, caPEM)
	if err != nil {
		return err
	}
	defer conn.Close()
	stream, err := cellarv1.NewSandboxRuntimeClient(conn).Logs(ctx, req)
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
		if err := send(chunk); err != nil {
			return err
		}
	}
}

// AgentRelayMetadata attaches sandbox-id metadata for AgentRelay.
func AgentRelayMetadata(sandboxID string) metadata.MD {
	return metadata.Pairs(SandboxIDMetadataKey, sandboxID)
}
