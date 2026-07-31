package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
)

const (
	guestAgentBinPath = "/usr/local/bin/cellar-agent"
	jobsFileName      = "jobs.json"
)

// Job records a detached command running inside a sandbox.
type Job struct {
	ID        string    `json:"id"`
	ExecID    string    `json:"exec_id"`
	Command   []string  `json:"command"`
	StartedAt time.Time `json:"started_at"`
}

// JobStatus is the observed state of a background job.
type JobStatus struct {
	ID       string
	Phase    string // running | exited | unknown
	ExitCode int
	Command  []string
	StartedAt time.Time
}

func jobsPath(dataDir, sandboxID string) string {
	return filepath.Join(SandboxHostDir(dataDir, sandboxID), jobsFileName)
}

func loadJobs(dataDir, sandboxID string) ([]Job, error) {
	path := jobsPath(dataDir, sandboxID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var jobs []Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func saveJobs(dataDir, sandboxID string, jobs []Job) error {
	if err := PrepareSandboxDir(dataDir, sandboxID); err != nil {
		return err
	}
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(jobsPath(dataDir, sandboxID), data, 0o600)
}

func mintJobID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// StartJob runs a command detached inside the sandbox via cellar-agent job start.
func (a *Agent) StartJob(ctx context.Context, sandboxID string, cmd []string) (*Job, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("command required")
	}
	cid, err := a.resolveContainerID(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	id, err := mintJobID()
	if err != nil {
		return nil, err
	}
	agentCmd := append([]string{guestAgentBinPath, "job", "start", "--id", id, "--"}, cmd...)
	execID, err := a.Driver.ExecDetached(ctx, cid, agentCmd)
	if err != nil {
		return nil, err
	}
	job := Job{
		ID:        id,
		ExecID:    execID,
		Command:   append([]string(nil), cmd...),
		StartedAt: time.Now().UTC(),
	}
	jobs, err := loadJobs(a.DataDir, sandboxID)
	if err != nil {
		return nil, err
	}
	jobs = append(jobs, job)
	if err := saveJobs(a.DataDir, sandboxID, jobs); err != nil {
		return nil, err
	}
	return &job, nil
}

// ListJobs returns recorded jobs for a sandbox.
func (a *Agent) ListJobs(_ context.Context, sandboxID string) ([]Job, error) {
	return loadJobs(a.DataDir, sandboxID)
}

// JobStatus inspects a job's running state.
func (a *Agent) JobStatus(ctx context.Context, sandboxID, jobID string) (*JobStatus, error) {
	jobs, err := loadJobs(a.DataDir, sandboxID)
	if err != nil {
		return nil, err
	}
	var job *Job
	for i := range jobs {
		if jobs[i].ID == jobID {
			job = &jobs[i]
			break
		}
	}
	if job == nil {
		return nil, fmt.Errorf("job %s not found", jobID)
	}
	st := &JobStatus{
		ID:        job.ID,
		Command:   job.Command,
		StartedAt: job.StartedAt,
		Phase:     "unknown",
		ExitCode:  -1,
	}
	if job.ExecID != "" {
		running, exitCode, err := a.Driver.ExecInspectRunning(ctx, job.ExecID)
		if err == nil {
			if running {
				st.Phase = "running"
				return st, nil
			}
			st.Phase = "exited"
			st.ExitCode = exitCode
			return st, nil
		}
	}
	// Fallback: ask the in-sandbox agent.
	cid, err := a.resolveContainerID(ctx, sandboxID)
	if err != nil {
		return st, nil
	}
	out, err := a.runAgentJobCollect(ctx, cid, []string{guestAgentBinPath, "job", "status", jobID})
	if err != nil {
		return st, nil
	}
	line := strings.TrimSpace(out)
	if strings.Contains(line, "status=running") {
		st.Phase = "running"
	} else if strings.Contains(line, "status=exited") {
		st.Phase = "exited"
		if i := strings.Index(line, "exit_code="); i >= 0 {
			fmt.Sscanf(line[i:], "exit_code=%d", &st.ExitCode)
		}
	}
	return st, nil
}

// StopJob signals a background job to stop.
func (a *Agent) StopJob(ctx context.Context, sandboxID, jobID string, timeoutSec int) error {
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	cid, err := a.resolveContainerID(ctx, sandboxID)
	if err != nil {
		return err
	}
	cmd := []string{
		guestAgentBinPath, "job", "stop", jobID,
		"--timeout", fmt.Sprintf("%ds", timeoutSec),
	}
	_, err = a.runAgentJobCollect(ctx, cid, cmd)
	return err
}

// JobLogs streams job stdout/stderr via an attached exec of cellar-agent job logs.
func (a *Agent) JobLogs(ctx context.Context, sandboxID, jobID string, follow bool) (io.ReadCloser, error) {
	cid, err := a.resolveContainerID(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	cmd := []string{guestAgentBinPath, "job", "logs", jobID}
	if follow {
		cmd = append(cmd, "--follow")
	}
	sess, _, err := a.Driver.ExecSession(ctx, cid, cmd, false, false)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer sess.Close()
		_, err := stdcopy.StdCopy(pw, pw, sess)
		if err != nil && err != io.EOF {
			_ = pw.CloseWithError(err)
		}
	}()
	return pr, nil
}

func (a *Agent) runAgentJobCollect(ctx context.Context, containerID string, cmd []string) (string, error) {
	sess, _, err := a.Driver.ExecSession(ctx, containerID, cmd, false, false)
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var stdout, stderr strings.Builder
	errCh := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(&stdout, &stderr, sess)
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-errCh:
		if err != nil && err != io.EOF {
			return "", err
		}
	}
	code, errMsg := sess.Wait()
	if errMsg != "" {
		return "", fmt.Errorf("%s", errMsg)
	}
	if code != 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = fmt.Sprintf("exit code %d", code)
		}
		return stdout.String(), fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}
