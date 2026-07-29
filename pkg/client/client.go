// Package client is the public Go SDK for Cellar’s HTTP gateway.
//
// Authenticate with CELLAR_API_KEY (or Config.APIKey). Point CELLAR_ENDPOINT /
// Config.Endpoint at the gateway base URL (e.g. https://cellar.example.com).
package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

const (
	EnvAPIKey   = "CELLAR_API_KEY"
	EnvEndpoint = "CELLAR_ENDPOINT"

	// Deprecated: use EnvEndpoint. Kept so old env dumps are recognizable.
	EnvEndpoints = "CELLAR_ENDPOINTS"
	// Deprecated: cluster CA is no longer required for HTTP clients.
	EnvCACert = "CELLAR_CA_CERT"
)

var (
	protoMarshal = protojson.MarshalOptions{
		UseProtoNames:   false,
		EmitUnpopulated: false,
	}
	protoUnmarshal = protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
)

// Config configures a Client.
type Config struct {
	// Endpoint is the gateway base URL (e.g. https://cellar.example.com).
	Endpoint string
	// APIKey is a cellar_… secret. Required.
	APIKey string
	// HTTPClient is used for requests; defaults to http.DefaultClient.
	HTTPClient *http.Client
	// Timeout applies when HTTPClient is nil (default 60s).
	Timeout time.Duration
}

// Client talks to the Cellar HTTP gateway.
type Client struct {
	cfg    Config
	http   *http.Client
	base   string
}

// NewFromEnv builds a client from CELLAR_API_KEY and CELLAR_ENDPOINT.
func NewFromEnv() (*Client, error) {
	return New(Config{
		Endpoint: os.Getenv(EnvEndpoint),
		APIKey:   os.Getenv(EnvAPIKey),
	})
}

// New creates a Client.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key is required (set %s)", EnvAPIKey)
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is required (set %s)", EnvEndpoint)
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint %q: need absolute URL with scheme and host", endpoint)
	}
	base := strings.TrimRight(endpoint, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		hc = &http.Client{Timeout: timeout}
	}
	return &Client{cfg: cfg, http: hc, base: base}, nil
}

func (c *Client) authHeaders(h http.Header) {
	h.Set("Authorization", "Bearer "+c.cfg.APIKey)
	h.Set("X-Api-Key", c.cfg.APIKey)
}

type apiError struct {
	Status int
	Code   string
	Msg    string
}

func (e *apiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("cellar: %s (%s)", e.Msg, e.Code)
	}
	return fmt.Sprintf("cellar: %s (HTTP %d)", e.Msg, e.Status)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body proto.Message, out proto.Message) error {
	var rdr io.Reader
	if body != nil {
		b, err := protoMarshal.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	c.authHeaders(req.Header)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return protoUnmarshal.Unmarshal(data, out)
}

func parseAPIError(status int, data []byte) error {
	var eb struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(data, &eb)
	msg := eb.Error
	if msg == "" {
		msg = strings.TrimSpace(string(data))
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &apiError{Status: status, Code: eb.Code, Msg: msg}
}

// Create creates a sandbox.
func (c *Client) Create(ctx context.Context, req *cellarv1.SandboxCreateRequest) (*cellarv1.Sandbox, error) {
	out := &cellarv1.Sandbox{}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/sandboxes", req, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Stop stops a sandbox.
func (c *Client) Stop(ctx context.Context, id string) (*cellarv1.Sandbox, error) {
	out := &cellarv1.Sandbox{}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/sandboxes/"+url.PathEscape(id)+"/stop", nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Remove deletes a sandbox.
func (c *Client) Remove(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/v1/sandboxes/"+url.PathEscape(id), nil, nil)
}

// Get returns a sandbox.
func (c *Client) Get(ctx context.Context, id string) (*cellarv1.Sandbox, error) {
	out := &cellarv1.Sandbox{}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/sandboxes/"+url.PathEscape(id), nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// List returns all sandboxes.
func (c *Client) List(ctx context.Context) ([]*cellarv1.Sandbox, error) {
	out := &cellarv1.SandboxListResponse{}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/sandboxes", nil, out); err != nil {
		return nil, err
	}
	return out.Sandboxes, nil
}

// UpdateNetwork replaces a sandbox network policy.
func (c *Client) UpdateNetwork(ctx context.Context, req *cellarv1.SandboxUpdateNetworkRequest) (*cellarv1.Sandbox, error) {
	if req == nil || req.SandboxId == "" {
		return nil, fmt.Errorf("sandbox_id is required")
	}
	id := req.SandboxId
	out := &cellarv1.Sandbox{}
	if err := c.doJSON(ctx, http.MethodPut, "/v1/sandboxes/"+url.PathEscape(id)+"/network", req, out); err != nil {
		return nil, err
	}
	return out, nil
}

// LogsOptions configures a logs request.
type LogsOptions struct {
	Follow     bool
	Tail       int64
	Timestamps bool
}

// LogsChunk is one NDJSON log line from the gateway.
type LogsChunk struct {
	Data []byte
}

// Logs streams sandbox logs as NDJSON chunks until EOF or ctx cancel.
func (c *Client) Logs(ctx context.Context, id string, opt LogsOptions) (<-chan LogsChunk, <-chan error) {
	ch := make(chan LogsChunk)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		defer close(errCh)
		q := url.Values{}
		if opt.Follow {
			q.Set("follow", "true")
		}
		if opt.Timestamps {
			q.Set("timestamps", "true")
		}
		if opt.Tail != 0 {
			q.Set("tail", strconv.FormatInt(opt.Tail, 10))
		}
		path := "/v1/sandboxes/" + url.PathEscape(id) + "/logs"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			errCh <- err
			return
		}
		c.authHeaders(req.Header)
		req.Header.Set("Accept", "application/x-ndjson")

		// Streaming must not use a client-wide Timeout that kills long follows.
		hc := c.http
		if hc.Timeout != 0 {
			clone := *hc
			clone.Timeout = 0
			hc = &clone
		}
		resp, err := hc.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(resp.Body)
			errCh <- parseAPIError(resp.StatusCode, data)
			return
		}
		dec := json.NewDecoder(resp.Body)
		for {
			var row struct {
				Data string `json:"data"`
			}
			if err := dec.Decode(&row); err != nil {
				if err == io.EOF {
					return
				}
				errCh <- err
				return
			}
			raw, err := base64.StdEncoding.DecodeString(row.Data)
			if err != nil {
				errCh <- err
				return
			}
			select {
			case ch <- LogsChunk{Data: raw}:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
	}()
	return ch, errCh
}

// ExecResult is the outcome of a non-interactive exec.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int32
	Error    string
}

// Exec runs a command in a sandbox and collects output until exit.
func (c *Client) Exec(ctx context.Context, sandboxID string, command []string) (*ExecResult, error) {
	payload, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/v1/sandboxes/"+url.PathEscape(sandboxID)+"/exec",
		bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.authHeaders(req.Header)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseAPIError(resp.StatusCode, data)
	}
	var out struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int32  `json:"exitCode"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &ExecResult{
		Stdout:   []byte(out.Stdout),
		Stderr:   []byte(out.Stderr),
		ExitCode: out.ExitCode,
		Error:    out.Error,
	}, nil
}
