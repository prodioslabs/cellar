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

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
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
			_, _ = w.Write([]byte(`{"id":"sb1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes/sb1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sandboxes":[{"id":"sb1"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/sb1/stop":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v1/sandboxes/sb1/network":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sb1"}`))
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

	sb, err := c.Create(ctx, &cellarv1.SandboxCreateRequest{Spec: &cellarv1.SandboxSpec{Image: "alpine"}})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Id != "sb1" {
		t.Fatalf("id=%q", sb.Id)
	}
	if sawAuth != "Bearer cellar_secret" || sawKey != "cellar_secret" {
		t.Fatalf("auth=%q key=%q", sawAuth, sawKey)
	}

	if _, err := c.Get(ctx, "sb1"); err != nil {
		t.Fatal(err)
	}
	list, err := c.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %#v", err, list)
	}
	if _, err := c.Stop(ctx, "sb1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateNetwork(ctx, &cellarv1.SandboxUpdateNetworkRequest{SandboxId: "sb1"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(ctx, "sb1"); err != nil {
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
	ae, ok := err.(*apiError)
	if !ok || ae.Status != 404 || ae.Code != "not_found" {
		t.Fatalf("%#v", err)
	}
}

func TestClientExec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Exec(context.Background(), "sb1", []string{"echo", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "hi\n" || res.ExitCode != 0 {
		t.Fatalf("%#v", res)
	}
}

func TestClientLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		_ = enc.Encode(map[string]string{"data": base64.StdEncoding.EncodeToString([]byte("a\n"))})
		_ = enc.Encode(map[string]string{"data": base64.StdEncoding.EncodeToString([]byte("b\n"))})
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chunks, errCh := c.Logs(ctx, "sb1", LogsOptions{Tail: 10})
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

func TestClientLogsCancel(t *testing.T) {
	var started atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started.Store(true)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	chunks, errCh := c.Logs(ctx, "sb1", LogsOptions{Follow: true})
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
