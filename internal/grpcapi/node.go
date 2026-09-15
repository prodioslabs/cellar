package grpcapi

import (
	"context"

	"google.golang.org/grpc"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

// NodeControlHost serves cluster node reads for the public NodeControl API.
type NodeControlHost interface {
	NodeList(ctx context.Context, req *cellarv1.NodeListRequest) (*cellarv1.NodeListResponse, error)
	NodeInspect(ctx context.Context, req *cellarv1.NodeInspectRequest) (*cellarv1.NodeInspectResponse, error)
}

// NodeServer implements NodeControl on managers (mTLS).
type NodeServer struct {
	cellarv1.UnimplementedNodeControlServer

	host NodeControlHost
}

// NewNodeServer wires NodeControl onto a host that can list/inspect nodes.
func NewNodeServer(host NodeControlHost) *NodeServer {
	return &NodeServer{host: host}
}

func (s *NodeServer) List(ctx context.Context, req *cellarv1.NodeListRequest) (*cellarv1.NodeListResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if s.host == nil {
		return &cellarv1.NodeListResponse{}, nil
	}
	return s.host.NodeList(WithInternalCall(ctx), req)
}

func (s *NodeServer) Inspect(ctx context.Context, req *cellarv1.NodeInspectRequest) (*cellarv1.NodeInspectResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if s.host == nil {
		return &cellarv1.NodeInspectResponse{}, nil
	}
	return s.host.NodeInspect(WithInternalCall(ctx), req)
}

// RegisterNodeControl registers the public NodeControl service.
func RegisterNodeControl(s *grpc.Server, ns *NodeServer) {
	if ns != nil {
		cellarv1.RegisterNodeControlServer(s, ns)
	}
}
