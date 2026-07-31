package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

type fakeUpstream struct {
	mu sync.Mutex

	lastKey string
	create  *cellarv1.Sandbox
	get     *cellarv1.Sandbox
	list    []*cellarv1.Sandbox
	err     error
	ready   error

	logsChunks [][]byte
	logsErr    error
	logsBlock  bool
	execRes    *ExecResult
	execErr    error

	canceledLogs bool
}

func (f *fakeUpstream) Create(_ context.Context, apiKey string, _ *cellarv1.SandboxCreateRequest) (*cellarv1.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.create, nil
}

func (f *fakeUpstream) Stop(_ context.Context, apiKey, _ string) (*cellarv1.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.get, nil
}

func (f *fakeUpstream) Remove(_ context.Context, apiKey, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return f.err
}

func (f *fakeUpstream) Get(_ context.Context, apiKey, _ string) (*cellarv1.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.get, nil
}

func (f *fakeUpstream) List(_ context.Context, apiKey string) ([]*cellarv1.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

func (f *fakeUpstream) UpdateNetwork(_ context.Context, apiKey string, _ *cellarv1.SandboxUpdateNetworkRequest) (*cellarv1.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.get, nil
}

type fakeLogsStream struct {
	ctx    context.Context
	chunks [][]byte
	err    error
	i      int
	block  bool
	onDone func()
}

func (s *fakeLogsStream) Recv() (*cellarv1.SandboxLogsChunk, error) {
	if s.i < len(s.chunks) {
		ch := &cellarv1.SandboxLogsChunk{Data: s.chunks[s.i]}
		s.i++
		return ch, nil
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.block {
		<-s.ctx.Done()
		if s.onDone != nil {
			s.onDone()
		}
		return nil, s.ctx.Err()
	}
	return nil, io.EOF
}

func (s *fakeLogsStream) Close() error { return nil }

func (f *fakeUpstream) Logs(ctx context.Context, apiKey string, _ *cellarv1.SandboxLogsRequest) (LogsStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return &fakeLogsStream{
		ctx:    ctx,
		chunks: f.logsChunks,
		err:    f.logsErr,
		block:  f.logsBlock,
		onDone: func() {
			f.mu.Lock()
			f.canceledLogs = true
			f.mu.Unlock()
		},
	}, nil
}

func (f *fakeUpstream) Exec(_ context.Context, apiKey, _ string, _ []string) (*ExecResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.execErr != nil {
		return nil, f.execErr
	}
	return f.execRes, nil
}

func (f *fakeUpstream) StartJob(_ context.Context, apiKey, _ string, _ []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return "job1", nil
}

func (f *fakeUpstream) ListJobs(_ context.Context, apiKey, _ string) ([]*cellarv1.JobInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return nil, nil
}

func (f *fakeUpstream) GetJob(_ context.Context, apiKey, _, _ string) (*cellarv1.JobInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return &cellarv1.JobInfo{Id: "job1", Phase: "running"}, nil
}

func (f *fakeUpstream) StopJob(_ context.Context, apiKey, _, _ string, _ int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return nil
}

func (f *fakeUpstream) JobLogs(ctx context.Context, apiKey string, _ *cellarv1.JobLogsRequest) (LogsStream, error) {
	return f.Logs(ctx, apiKey, &cellarv1.SandboxLogsRequest{})
}

func (f *fakeUpstream) Ready(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func newTestServer(t *testing.T, up Upstream) *Server {
	t.Helper()
	s, err := New(Config{ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}, up)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHealthz(t *testing.T) {
	s := newTestServer(t, &fakeUpstream{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	up := &fakeUpstream{ready: status.Error(codes.Unavailable, "down")}
	s := newTestServer(t, up)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}

	up.ready = nil
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAuthRequired(t *testing.T) {
	s := newTestServer(t, &fakeUpstream{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAuthForwardBearer(t *testing.T) {
	up := &fakeUpstream{list: []*cellarv1.Sandbox{{Id: "sb1"}}}
	s := newTestServer(t, up)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes", nil)
	req.Header.Set("Authorization", "Bearer cellar_testkey")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	up.mu.Lock()
	got := up.lastKey
	up.mu.Unlock()
	if got != "cellar_testkey" {
		t.Fatalf("api key = %q", got)
	}
}

func TestAuthForwardXAPIKey(t *testing.T) {
	up := &fakeUpstream{get: &cellarv1.Sandbox{Id: "sb1"}}
	s := newTestServer(t, up)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/sb1", nil)
	req.Header.Set("X-Api-Key", "cellar_x")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	up.mu.Lock()
	got := up.lastKey
	up.mu.Unlock()
	if got != "cellar_x" {
		t.Fatalf("api key = %q", got)
	}
}

func TestCreateAndGRPCStatusMapping(t *testing.T) {
	up := &fakeUpstream{
		create: &cellarv1.Sandbox{Id: "new"},
	}
	s := newTestServer(t, up)
	body := `{"spec":{"image":"alpine:3.20"}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer k")
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var sb cellarv1.Sandbox
	if err := protojson.Unmarshal(rec.Body.Bytes(), &sb); err != nil {
		t.Fatal(err)
	}
	if sb.Id != "new" {
		t.Fatalf("id = %q", sb.Id)
	}

	up.err = status.Error(codes.NotFound, "missing")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/sandboxes/x", nil)
	req.Header.Set("Authorization", "Bearer k")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDeleteNoContent(t *testing.T) {
	s := newTestServer(t, &fakeUpstream{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/sb1", nil)
	req.Header.Set("Authorization", "Bearer k")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestExec(t *testing.T) {
	up := &fakeUpstream{
		execRes: &ExecResult{Stdout: []byte("hi\n"), ExitCode: 0},
	}
	s := newTestServer(t, up)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/sb1/exec", strings.NewReader(`{"command":["echo","hi"]}`))
	req.Header.Set("Authorization", "Bearer k")
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out execResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Stdout != "hi\n" || out.ExitCode != 0 {
		t.Fatalf("unexpected %#v", out)
	}
}

func TestLogsNDJSON(t *testing.T) {
	up := &fakeUpstream{
		logsChunks: [][]byte{[]byte("line1\n"), []byte("line2\n")},
	}
	s := newTestServer(t, up)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/sb1/logs?tail=10", nil)
	req.Header.Set("Authorization", "Bearer k")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/x-ndjson") {
		t.Fatalf("content-type = %q", ct)
	}
	lines := bytes.Split(bytes.TrimSpace(rec.Body.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("lines = %d body=%s", len(lines), rec.Body.String())
	}
	var row map[string]string
	if err := json.Unmarshal(lines[0], &row); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(row["data"])
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "line1\n" {
		t.Fatalf("data = %q", raw)
	}
}

func TestLogsCancel(t *testing.T) {
	up := &fakeUpstream{logsBlock: true}
	s := newTestServer(t, up)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/sandboxes/sb1/logs?follow=true", nil)
	req.Header.Set("Authorization", "Bearer k")

	done := make(chan struct{})
	go func() {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after cancel")
	}
	up.mu.Lock()
	canceled := up.canceledLogs
	up.mu.Unlock()
	if !canceled {
		t.Fatal("expected stream to observe cancel")
	}
}

func TestParseUpstreams(t *testing.T) {
	got := ParseUpstreams(" a:1, b:2 ,,c:3 ")
	if len(got) != 3 || got[0] != "a:1" || got[2] != "c:3" {
		t.Fatalf("%v", got)
	}
}

func TestGRPCCodeToHTTP(t *testing.T) {
	cases := map[codes.Code]int{
		codes.NotFound:         http.StatusNotFound,
		codes.Unauthenticated:  http.StatusUnauthorized,
		codes.PermissionDenied: http.StatusForbidden,
		codes.InvalidArgument:  http.StatusBadRequest,
		codes.Unavailable:      http.StatusServiceUnavailable,
	}
	for code, want := range cases {
		if got := grpcCodeToHTTP(code); got != want {
			t.Fatalf("%v: got %d want %d", code, got, want)
		}
	}
}
