package gateway

import (
	"bytes"
	"context"
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

	logsChunks []*cellarv1.SandboxLogsChunk
	logsErr    error
	logsBlock  bool
	canceledLogs bool

	volumes []*cellarv1.Volume
	volume  *cellarv1.Volume
}

func (f *fakeUpstream) Create(_ context.Context, apiKey string, _ []byte, _ bool) (*cellarv1.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.create, nil
}

func (f *fakeUpstream) Start(_ context.Context, apiKey, _ string) (*cellarv1.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	if f.get != nil {
		return f.get, nil
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

func (f *fakeUpstream) GetByName(_ context.Context, apiKey, _ string) (*cellarv1.Sandbox, error) {
	return f.Get(context.Background(), apiKey, "")
}

func (f *fakeUpstream) List(_ context.Context, apiKey, _ string, _ uint32, _ string) ([]*cellarv1.Sandbox, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, "", f.err
	}
	return f.list, "", nil
}

type fakeLogsStream struct {
	ctx    context.Context
	chunks []*cellarv1.SandboxLogsChunk
	err    error
	i      int
	block  bool
	onDone func()
}

func (s *fakeLogsStream) Recv() (*cellarv1.SandboxLogsChunk, error) {
	if s.i < len(s.chunks) {
		ch := s.chunks[s.i]
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

type fakeAgentRelay struct{}

func (f *fakeAgentRelay) Send([]byte) error      { return io.EOF }
func (f *fakeAgentRelay) Recv() ([]byte, error)  { return nil, io.EOF }
func (f *fakeAgentRelay) Close() error           { return nil }

func (f *fakeUpstream) AgentRelay(_ context.Context, apiKey, _ string) (AgentRelayStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return &fakeAgentRelay{}, nil
}

func (f *fakeUpstream) CreateVolume(_ context.Context, apiKey string, _ *cellarv1.VolumeCreateRequest) (*cellarv1.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.volume, nil
}

func (f *fakeUpstream) ListVolumes(_ context.Context, apiKey string) ([]*cellarv1.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.volumes, nil
}

func (f *fakeUpstream) GetVolume(_ context.Context, apiKey, _ string) (*cellarv1.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return f.volume, nil
}

func (f *fakeUpstream) GetDefaultVolume(_ context.Context, apiKey string) (*cellarv1.Volume, error) {
	return f.GetVolume(context.Background(), apiKey, "")
}

func (f *fakeUpstream) DeleteVolume(_ context.Context, apiKey, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return "", f.err
	}
	return "volume deleted", nil
}

func (f *fakeUpstream) VolumeFsRead(_ context.Context, apiKey, _, _ string) (FsStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return &fakeFsStream{}, nil
}

func (f *fakeUpstream) VolumeFsWrite(_ context.Context, apiKey, _, _ string, _ io.Reader) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return f.err
}

func (f *fakeUpstream) VolumeFsStat(_ context.Context, apiKey, _, _ string) (*cellarv1.FsMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return &cellarv1.FsMetadata{Kind: "file", Size: 1, Mode: 0o644}, nil
}

func (f *fakeUpstream) VolumeFsList(_ context.Context, apiKey, _, _ string) ([]*cellarv1.FsEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	if f.err != nil {
		return nil, f.err
	}
	return []*cellarv1.FsEntry{{Path: "/a", Kind: "file"}}, nil
}

func (f *fakeUpstream) VolumeFsExists(_ context.Context, apiKey, _, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return true, f.err
}

func (f *fakeUpstream) VolumeFsMkdir(_ context.Context, apiKey, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return f.err
}

func (f *fakeUpstream) VolumeFsRemove(_ context.Context, apiKey, _, _ string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return f.err
}

func (f *fakeUpstream) VolumeFsCopy(_ context.Context, apiKey, _, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return f.err
}

func (f *fakeUpstream) VolumeFsRename(_ context.Context, apiKey, _, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKey = apiKey
	return f.err
}

type fakeFsStream struct{}

func (s *fakeFsStream) Recv() (*cellarv1.FsChunk, error) { return nil, io.EOF }
func (s *fakeFsStream) Close() error                     { return nil }

func (f *fakeUpstream) Ready(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *fakeUpstream) ClusterID() string { return "local" }

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
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "unauthenticated" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestAuthForwardBearer(t *testing.T) {
	up := &fakeUpstream{list: []*cellarv1.Sandbox{{Id: "sb1", Name: "demo", SpecJson: []byte(`{"name":"demo","image":{"type":"oci","reference":"alpine"}}`)}}}
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
	var page cloudListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != "sb1" {
		t.Fatalf("page = %#v", page)
	}
}

func TestAuthForwardXAPIKey(t *testing.T) {
	up := &fakeUpstream{get: &cellarv1.Sandbox{Id: "sb1", Name: "n", Status: &cellarv1.SandboxStatus{Phase: "running"}}}
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
	var sb cloudSandboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sb); err != nil {
		t.Fatal(err)
	}
	if sb.Status != "running" || sb.OrgID != "local" {
		t.Fatalf("sb = %#v", sb)
	}
}

func TestCreateAndGRPCStatusMapping(t *testing.T) {
	up := &fakeUpstream{
		create: &cellarv1.Sandbox{Id: "new", Name: "demo", SpecJson: []byte(`{"name":"demo","image":{"type":"oci","reference":"alpine:3.20"}}`)},
	}
	s := newTestServer(t, up)
	body := `{"name":"demo","image":{"type":"oci","reference":"alpine:3.20"},"resources":{"vcpus":1,"memory_mib":512},"runtime":{},"network":{"enabled":false},"lifecycle":{}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer k")
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var sb cloudSandboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sb); err != nil {
		t.Fatal(err)
	}
	if sb.ID != "new" {
		t.Fatalf("id = %q", sb.ID)
	}

	up.err = status.Error(codes.NotFound, "missing")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/sandboxes/x", nil)
	req.Header.Set("Authorization", "Bearer k")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var errBody errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error.Code != "sandbox_not_found" {
		t.Fatalf("code = %q", errBody.Error.Code)
	}
}

func TestDeleteMessage(t *testing.T) {
	s := newTestServer(t, &fakeUpstream{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/sb1", nil)
	req.Header.Set("Authorization", "Bearer k")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var msg messageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Message == "" {
		t.Fatal("empty message")
	}
}

func TestLogsSSE(t *testing.T) {
	up := &fakeUpstream{
		logsChunks: []*cellarv1.SandboxLogsChunk{
			{Id: "1", Source: "stdout", TsUnixNano: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano(), Text: "line1\n"},
		},
	}
	s := newTestServer(t, up)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/sb1/logs", nil)
	req.Header.Set("Authorization", "Bearer k")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: log") || !strings.Contains(body, `"text":"line1\n"`) {
		t.Fatalf("body = %s", body)
	}
	if !strings.Contains(body, "event: end") {
		t.Fatalf("missing end event: %s", body)
	}
}

func TestLogsCancel(t *testing.T) {
	up := &fakeUpstream{logsBlock: true}
	s := newTestServer(t, up)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/sandboxes/sb1/logs", nil)
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

func TestVolumeList(t *testing.T) {
	up := &fakeUpstream{
		volumes: []*cellarv1.Volume{{Id: "v1", Name: "data", Kind: "named", Status: "ready"}},
	}
	s := newTestServer(t, up)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/volumes", nil)
	req.Header.Set("Authorization", "Bearer k")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"id":"v1"`)) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
