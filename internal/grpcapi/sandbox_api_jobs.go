package grpcapi

import (
	"context"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

func (s *SandboxAPIServer) dialOwningRuntime(ctx context.Context, sandboxID string) (cellarv1.SandboxRuntimeClient, func(), error) {
	sbResp, err := s.Get(ctx, &cellarv1.SandboxGetRequest{SandboxId: sandboxID})
	if err != nil {
		return nil, nil, err
	}
	if sbResp.Sandbox == nil || sbResp.Sandbox.NodeId == "" {
		return nil, nil, status.Error(codes.FailedPrecondition, "sandbox has no owning node")
	}
	addr, err := s.host.RuntimeAddr(ctx, sbResp.Sandbox.NodeId)
	if err != nil {
		return nil, nil, status.Error(codes.Unavailable, err.Error())
	}
	cert, key, ca, err := s.host.IdentityPEMs()
	if err != nil {
		return nil, nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	conn, err := DialRuntime(addr, cert, key, ca)
	if err != nil {
		return nil, nil, err
	}
	return cellarv1.NewSandboxRuntimeClient(conn), func() { _ = conn.Close() }, nil
}

func (s *SandboxAPIServer) StartJob(ctx context.Context, req *cellarv1.StartJobRequest) (*cellarv1.StartJobResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.StartJob(ctx, req)
}

func (s *SandboxAPIServer) ListJobs(ctx context.Context, req *cellarv1.ListJobsRequest) (*cellarv1.ListJobsResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.ListJobs(ctx, req)
}

func (s *SandboxAPIServer) GetJob(ctx context.Context, req *cellarv1.GetJobRequest) (*cellarv1.GetJobResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.GetJob(ctx, req)
}

func (s *SandboxAPIServer) StopJob(ctx context.Context, req *cellarv1.StopJobRequest) (*cellarv1.StopJobResponse, error) {
	rt, closeFn, err := s.dialOwningRuntime(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeFn()
	return rt.StopJob(ctx, req)
}

func (s *SandboxAPIServer) JobLogs(req *cellarv1.JobLogsRequest, stream cellarv1.SandboxAPI_JobLogsServer) error {
	rt, closeFn, err := s.dialOwningRuntime(stream.Context(), req.GetSandboxId())
	if err != nil {
		return err
	}
	defer closeFn()
	remote, err := rt.JobLogs(stream.Context(), req)
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
