package grpcapi

import (
	"context"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/runtime"
)

// JobRuntime is the local job control surface used by SandboxRuntime.
type JobRuntime interface {
	StartJob(ctx context.Context, sandboxID string, cmd []string) (*runtime.Job, error)
	ListJobs(ctx context.Context, sandboxID string) ([]runtime.Job, error)
	JobStatus(ctx context.Context, sandboxID, jobID string) (*runtime.JobStatus, error)
	StopJob(ctx context.Context, sandboxID, jobID string, timeoutSec int) error
	JobLogs(ctx context.Context, sandboxID, jobID string, follow bool) (io.ReadCloser, error)
}

func jobInfoFromStatus(st *runtime.JobStatus) *cellarv1.JobInfo {
	if st == nil {
		return nil
	}
	return &cellarv1.JobInfo{
		Id:                 st.ID,
		Command:            st.Command,
		Phase:              st.Phase,
		ExitCode:           int32(st.ExitCode),
		StartedAtUnixNano:  st.StartedAt.UnixNano(),
	}
}

func (r *RuntimeServer) StartJob(ctx context.Context, req *cellarv1.StartJobRequest) (*cellarv1.StartJobResponse, error) {
	jr, ok := r.local.(JobRuntime)
	if !ok || r.local == nil {
		return nil, status.Error(codes.Unavailable, "runtime not ready")
	}
	if req.SandboxId == "" || len(req.Command) == 0 {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id and command required")
	}
	job, err := jr.StartJob(ctx, req.SandboxId, req.Command)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &cellarv1.StartJobResponse{JobId: job.ID}, nil
}

func (r *RuntimeServer) ListJobs(ctx context.Context, req *cellarv1.ListJobsRequest) (*cellarv1.ListJobsResponse, error) {
	jr, ok := r.local.(JobRuntime)
	if !ok || r.local == nil {
		return nil, status.Error(codes.Unavailable, "runtime not ready")
	}
	if req.SandboxId == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id required")
	}
	jobs, err := jr.ListJobs(ctx, req.SandboxId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &cellarv1.ListJobsResponse{}
	for _, j := range jobs {
		st, err := jr.JobStatus(ctx, req.SandboxId, j.ID)
		if err != nil {
			out.Jobs = append(out.Jobs, &cellarv1.JobInfo{
				Id:                j.ID,
				Command:           j.Command,
				Phase:             "unknown",
				StartedAtUnixNano: j.StartedAt.UnixNano(),
			})
			continue
		}
		out.Jobs = append(out.Jobs, jobInfoFromStatus(st))
	}
	return out, nil
}

func (r *RuntimeServer) GetJob(ctx context.Context, req *cellarv1.GetJobRequest) (*cellarv1.GetJobResponse, error) {
	jr, ok := r.local.(JobRuntime)
	if !ok || r.local == nil {
		return nil, status.Error(codes.Unavailable, "runtime not ready")
	}
	if req.SandboxId == "" || req.JobId == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id and job_id required")
	}
	st, err := jr.JobStatus(ctx, req.SandboxId, req.JobId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &cellarv1.GetJobResponse{Job: jobInfoFromStatus(st)}, nil
}

func (r *RuntimeServer) StopJob(ctx context.Context, req *cellarv1.StopJobRequest) (*cellarv1.StopJobResponse, error) {
	jr, ok := r.local.(JobRuntime)
	if !ok || r.local == nil {
		return nil, status.Error(codes.Unavailable, "runtime not ready")
	}
	if req.SandboxId == "" || req.JobId == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id and job_id required")
	}
	if err := jr.StopJob(ctx, req.SandboxId, req.JobId, int(req.TimeoutSec)); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &cellarv1.StopJobResponse{}, nil
}

func (r *RuntimeServer) JobLogs(req *cellarv1.JobLogsRequest, stream cellarv1.SandboxRuntime_JobLogsServer) error {
	jr, ok := r.local.(JobRuntime)
	if !ok || r.local == nil {
		return status.Error(codes.Unavailable, "runtime not ready")
	}
	if req.SandboxId == "" || req.JobId == "" {
		return status.Error(codes.InvalidArgument, "sandbox_id and job_id required")
	}
	rc, err := jr.JobLogs(stream.Context(), req.SandboxId, req.JobId, req.Follow)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	defer rc.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			if serr := stream.Send(&cellarv1.SandboxLogsChunk{Data: append([]byte(nil), buf[:n]...)}); serr != nil {
				return serr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	}
}
