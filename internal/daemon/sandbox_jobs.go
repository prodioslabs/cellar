package daemon

import (
	"context"
	"fmt"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/grpcapi"
)

func (d *Daemon) withSandboxRuntime(ctx context.Context, sandboxID string, local func() error, remote func(cellarv1.SandboxRuntimeClient) error) error {
	sbResp, err := d.SandboxGet(ctx, &cellarv1.SandboxGetRequest{SandboxId: sandboxID})
	if err != nil {
		return err
	}
	mat := d.idStore.Material()
	if mat != nil && sbResp.Sandbox.NodeId == mat.NodeID && d.agent != nil {
		return local()
	}
	addr, err := d.lookupNodeRuntimeAddr(ctx, sbResp.Sandbox.NodeId)
	if err != nil {
		return err
	}
	if mat == nil {
		return fmt.Errorf("no identity")
	}
	conn, err := grpcapi.DialRuntime(addr, mat.Certificate, mat.PrivateKey, mat.CACert)
	if err != nil {
		return err
	}
	defer conn.Close()
	return remote(cellarv1.NewSandboxRuntimeClient(conn))
}

func (d *Daemon) SandboxStartJob(ctx context.Context, req *cellarv1.StartJobRequest) (*cellarv1.StartJobResponse, error) {
	var resp *cellarv1.StartJobResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		job, err := d.agent.StartJob(ctx, req.SandboxId, req.Command)
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		resp = &cellarv1.StartJobResponse{JobId: job.ID}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.StartJob(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxListJobs(ctx context.Context, req *cellarv1.ListJobsRequest) (*cellarv1.ListJobsResponse, error) {
	var resp *cellarv1.ListJobsResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		jobs, err := d.agent.ListJobs(ctx, req.SandboxId)
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		out := &cellarv1.ListJobsResponse{}
		for _, j := range jobs {
			st, err := d.agent.JobStatus(ctx, req.SandboxId, j.ID)
			if err != nil {
				out.Jobs = append(out.Jobs, &cellarv1.JobInfo{
					Id:                j.ID,
					Command:           j.Command,
					Phase:             "unknown",
					StartedAtUnixNano: j.StartedAt.UnixNano(),
				})
				continue
			}
			out.Jobs = append(out.Jobs, &cellarv1.JobInfo{
				Id:                st.ID,
				Command:           st.Command,
				Phase:             st.Phase,
				ExitCode:          int32(st.ExitCode),
				StartedAtUnixNano: st.StartedAt.UnixNano(),
			})
		}
		resp = out
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.ListJobs(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxGetJob(ctx context.Context, req *cellarv1.GetJobRequest) (*cellarv1.GetJobResponse, error) {
	var resp *cellarv1.GetJobResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		st, err := d.agent.JobStatus(ctx, req.SandboxId, req.JobId)
		if err != nil {
			return status.Error(codes.NotFound, err.Error())
		}
		resp = &cellarv1.GetJobResponse{Job: &cellarv1.JobInfo{
			Id:                st.ID,
			Command:           st.Command,
			Phase:             st.Phase,
			ExitCode:          int32(st.ExitCode),
			StartedAtUnixNano: st.StartedAt.UnixNano(),
		}}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.GetJob(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxStopJob(ctx context.Context, req *cellarv1.StopJobRequest) (*cellarv1.StopJobResponse, error) {
	var resp *cellarv1.StopJobResponse
	err := d.withSandboxRuntime(ctx, req.GetSandboxId(), func() error {
		if err := d.agent.StopJob(ctx, req.SandboxId, req.JobId, int(req.TimeoutSec)); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		resp = &cellarv1.StopJobResponse{}
		return nil
	}, func(rt cellarv1.SandboxRuntimeClient) error {
		var err error
		resp, err = rt.StopJob(ctx, req)
		return err
	})
	return resp, err
}

func (d *Daemon) SandboxJobLogs(req *cellarv1.JobLogsRequest, stream cellarv1.Control_SandboxJobLogsServer) error {
	return d.withSandboxRuntime(stream.Context(), req.GetSandboxId(), func() error {
		rc, err := d.agent.JobLogs(stream.Context(), req.SandboxId, req.JobId, req.Follow)
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
	}, func(rt cellarv1.SandboxRuntimeClient) error {
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
	})
}
