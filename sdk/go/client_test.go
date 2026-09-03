package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewRequiresEndpointAndKey(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := New(Config{APIKey: "k"}); err == nil {
		t.Fatal("expected endpoint error")
	}
	if _, err := New(Config{APIKey: "k", Endpoint: "not-a-url"}); err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestClientCRUDAndAuthHeaders(t *testing.T) {
	var sawAuth, sawKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawKey = r.Header.Get("X-Api-Key")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"pending"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sb1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"running"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sandboxes":[{"id":"sb1","desiredState":"running","status":{"phase":"running"}}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/sb1/stop":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"stopped","status":{"phase":"stopped"}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v1/sandboxes/sb1/network":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"running"}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/sb1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "cellar_secret", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	sb, err := c.Create(ctx, &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	if sb.ID() != "sb1" {
		t.Fatalf("id=%q", sb.ID())
	}
	if sb.Status() == nil || sb.Status().Phase != "pending" {
		t.Fatalf("status=%#v", sb.Status())
	}
	if sawAuth != "Bearer cellar_secret" || sawKey != "cellar_secret" {
		t.Fatalf("auth=%q key=%q", sawAuth, sawKey)
	}

	got, err := c.Get(ctx, "sb1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "sb1" {
		t.Fatalf("id=%q", got.ID())
	}
	list, err := c.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID() != "sb1" {
		t.Fatalf("list: %v %#v", err, list)
	}
	if err := sb.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if sb.DesiredState() != "stopped" {
		t.Fatalf("desired=%q", sb.DesiredState())
	}
	if err := sb.UpdateNetwork(ctx, &NetworkPolicy{Mode: "none"}); err != nil {
		t.Fatal(err)
	}
	if err := sb.Remove(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClientHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"missing","code":"not_found"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
	ae, ok := err.(*APIError)
	if !ok || ae.Status != 404 || ae.Code != "not_found" {
		t.Fatalf("%#v", err)
	}
}

func TestSandboxExec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"running"}}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec"):
			body, _ := io.ReadAll(r.Body)
			var req struct {
				Command []string `json:"command"`
			}
			_ = json.Unmarshal(body, &req)
			if len(req.Command) != 2 || req.Command[0] != "echo" {
				t.Errorf("command=%v", req.Command)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":   "hi\n",
				"stderr":   "",
				"exitCode": 0,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Create(context.Background(), &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := sb.Exec(context.Background(), []string{"echo", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "hi\n" || res.ExitCode != 0 {
		t.Fatalf("%#v", res)
	}
}

func TestSandboxLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"running"}}`))
		default:
			w.Header().Set("Content-Type", "application/x-ndjson")
			enc := json.NewEncoder(w)
			_ = enc.Encode(map[string]string{"data": base64.StdEncoding.EncodeToString([]byte("a\n"))})
			_ = enc.Encode(map[string]string{"data": base64.StdEncoding.EncodeToString([]byte("b\n"))})
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Create(context.Background(), &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chunks, errCh := sb.Logs(ctx, LogsOptions{Tail: 10})
	var got []string
	for ch := range chunks {
		got = append(got, string(ch.Data))
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "") != "a\nb\n" {
		t.Fatalf("%v", got)
	}
}

func TestSandboxLogsCancel(t *testing.T) {
	var started atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"running"}}`))
		default:
			started.Store(true)
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Create(context.Background(), &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	chunks, errCh := sb.Logs(ctx, LogsOptions{Follow: true})
	deadline := time.Now().Add(2 * time.Second)
	for !started.Load() {
		if time.Now().After(deadline) {
			t.Fatal("handler not started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	for range chunks {
	}
	err = <-errCh
	if err == nil {
		// EOF on cancel is also acceptable depending on timing.
		return
	}
	if err != context.Canceled && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err=%v", err)
	}
}

func TestSandboxGetStatus(t *testing.T) {
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"pending"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sb1":
			gets.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"running","containerId":"c1","message":"up"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Create(context.Background(), &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Status().Phase != "pending" {
		t.Fatalf("phase=%q", sb.Status().Phase)
	}
	st, err := sb.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != "running" || st.ContainerID != "c1" {
		t.Fatalf("%#v", st)
	}
	if sb.Status().Phase != "running" {
		t.Fatalf("local status not refreshed")
	}
	if gets.Load() != 1 {
		t.Fatalf("gets=%d", gets.Load())
	}
}

func TestSandboxWaitUntilReady(t *testing.T) {
	phases := []string{"pending", "starting", "failed", "running"}
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"pending"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sb1":
			i := int(gets.Add(1) - 1)
			if i >= len(phases) {
				i = len(phases) - 1
			}
			phase := phases[i]
			msg := ""
			if phase == "failed" {
				msg = "retrying"
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"` + phase + `","message":"` + msg + `"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Create(context.Background(), &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sb.WaitUntilReady(context.Background(), WaitUntilReadyOptions{
		Timeout:      5 * time.Second,
		PollInterval: time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	if sb.Status().Phase != "running" {
		t.Fatalf("phase=%q", sb.Status().Phase)
	}
	if gets.Load() != 4 {
		t.Fatalf("gets=%d", gets.Load())
	}
}

func TestSandboxWaitUntilReadyTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"pending"}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"starting","message":"booting"}}`))
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Create(context.Background(), &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	err = sb.WaitUntilReady(context.Background(), WaitUntilReadyOptions{
		Timeout:      20 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "not ready within") {
		t.Fatalf("err=%v", err)
	}
}

func TestSandboxWaitUntilReadyDesiredStopped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"pending"}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"stopped","status":{"phase":"pending","message":"stopping"}}`))
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Create(context.Background(), &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	err = sb.WaitUntilReady(context.Background(), WaitUntilReadyOptions{
		Timeout:      time.Second,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "desiredState=stopped") {
		t.Fatalf("err=%v", err)
	}
}

func TestSandboxWaitUntilReadyPhaseStopped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"pending"}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"stopped","message":"exited"}}`))
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Create(context.Background(), &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	err = sb.WaitUntilReady(context.Background(), WaitUntilReadyOptions{
		Timeout:      time.Second,
		PollInterval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "is stopped") {
		t.Fatalf("err=%v", err)
	}
}

func TestSandboxWaitUntilReadyCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"pending"}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"starting"}}`))
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Create(context.Background(), &SandboxCreateRequest{Spec: &SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = sb.WaitUntilReady(ctx, WaitUntilReadyOptions{
		Timeout:      5 * time.Second,
		PollInterval: 100 * time.Millisecond,
	})
	if err == nil || (err != context.Canceled && !strings.Contains(err.Error(), "canceled")) {
		t.Fatalf("err=%v", err)
	}
}

func TestCreateMarshalsNativeJSON(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"sb1"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Create(context.Background(), &SandboxCreateRequest{
		Spec: &SandboxSpec{
			Image:      "alpine:3.20",
			WorkingDir: "/work",
			Network:    &NetworkPolicy{Mode: "none"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	spec, _ := got["spec"].(map[string]any)
	if spec["image"] != "alpine:3.20" || spec["workingDir"] != "/work" {
		t.Fatalf("body=%s", body)
	}
}

func TestCreateMarshalsNetworkSugar(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"sb1"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	block := true
	_, err = c.Create(context.Background(), &SandboxCreateRequest{
		Spec: &SandboxSpec{
			Image: "alpine:3.20",
			Network: &NetworkPolicy{
				DomainAllowList:   "example.com,*.openai.com",
				EssentialServices: true,
				BlockAll:          &block, // mutually exclusive server-side; just checking JSON keys
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	spec, _ := got["spec"].(map[string]any)
	net, _ := spec["network"].(map[string]any)
	if net["domainAllowList"] != "example.com,*.openai.com" {
		t.Fatalf("network=%v", net)
	}
	if net["essentialServices"] != true {
		t.Fatalf("network=%v", net)
	}
	if net["blockAll"] != true {
		t.Fatalf("network=%v", net)
	}
}

func TestSandboxFS(t *testing.T) {
	var wrote []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sb1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1","desiredState":"running","status":{"phase":"running"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sb1/fs/content":
			_, _ = w.Write([]byte("hello"))
		case r.Method == http.MethodPut && r.URL.Path == "/v1/sandboxes/sb1/fs/content":
			wrote, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sb1/fs/stat":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kind":"file","size":5,"mode":420,"readonly":false,"modified":"2024-01-02T03:04:05Z","created":null}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sb1/fs/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"entries":[{"path":"/tmp/a","kind":"file","size":5,"mode":420,"modified":null}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sb1/fs/exists":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"exists":true}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/fs/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sb, err := c.Get(ctx, "sb1")
	if err != nil {
		t.Fatal(err)
	}
	fs := sb.FS()
	b, err := fs.Read(ctx, "/tmp/a")
	if err != nil || string(b) != "hello" {
		t.Fatalf("read=%q err=%v", b, err)
	}
	if err := fs.Write(ctx, "/tmp/a", []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if string(wrote) != "hi" {
		t.Fatalf("wrote=%q", wrote)
	}
	meta, err := fs.Stat(ctx, "/tmp/a")
	if err != nil || meta.Kind != FsKindFile || meta.Size != 5 {
		t.Fatalf("stat=%#v err=%v", meta, err)
	}
	entries, err := fs.List(ctx, "/tmp")
	if err != nil || len(entries) != 1 {
		t.Fatalf("list=%v err=%v", entries, err)
	}
	ok, err := fs.Exists(ctx, "/tmp/a")
	if err != nil || !ok {
		t.Fatalf("exists=%v err=%v", ok, err)
	}
	if err := fs.Mkdir(ctx, "/tmp/d"); err != nil {
		t.Fatal(err)
	}
}
