// Package client is the public Go SDK for Cellar's manager SandboxAPI.
//
// Authenticate with CELLAR_API_KEY (or Config.APIKey). Dial one or more manager
// gRPC addresses via CELLAR_ENDPOINTS / Config.Endpoints; the client round-robins
// and fails over on dial errors and Unavailable/DeadlineExceeded.
package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/grpcapi"
)

const (
	EnvAPIKey    = "CELLAR_API_KEY"
	EnvEndpoints = "CELLAR_ENDPOINTS"
	EnvCACert    = "CELLAR_CA_CERT"

	defaultMaxAttempts = 3
	unhealthyFor       = 5 * time.Second
)

// Config configures a Client.
type Config struct {
	// Endpoints are manager gRPC addresses (host:port). Required unless
	Endpoints []string
	// APIKey is a cellar_… secret. Required.
	APIKey string
	// CACert is the cluster CA PEM used to verify managers.
	CACert []byte
	// CACertFile loads CACert from disk when CACert is empty.
	CACertFile string
	// MaxAttempts caps failover tries per RPC (default 3).
	MaxAttempts int
}

// Client talks to SandboxAPI with multi-endpoint failover.
type Client struct {
	cfg     Config
	tlsCfg  *tls.Config
	rr      atomic.Uint64
	mu      sync.Mutex
	lastOK  string
	badUntil map[string]time.Time
}

// NewFromEnv builds a client from CELLAR_* environment variables.
func NewFromEnv() (*Client, error) {
	return New(Config{
		Endpoints:  splitEndpoints(os.Getenv(EnvEndpoints)),
		APIKey:     os.Getenv(EnvAPIKey),
		CACertFile: os.Getenv(EnvCACert),
	})
}

// New creates a Client.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key is required (set %s)", EnvAPIKey)
	}
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("at least one endpoint is required (set %s)", EnvEndpoints)
	}
	for i, e := range cfg.Endpoints {
		cfg.Endpoints[i] = strings.TrimSpace(e)
	}
	caPEM := cfg.CACert
	if len(caPEM) == 0 && cfg.CACertFile != "" {
		b, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		caPEM = b
	}
	if len(caPEM) == 0 {
		return nil, fmt.Errorf("CA certificate is required (set %s or Config.CACert)", EnvCACert)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.MaxAttempts > len(cfg.Endpoints)*2 {
		cfg.MaxAttempts = len(cfg.Endpoints) * 2
	}
	return &Client{
		cfg: cfg,
		tlsCfg: &tls.Config{
			RootCAs:    pool,
			ServerName: grpcapi.TLSServerName,
			MinVersion: tls.VersionTLS12,
		},
		badUntil: make(map[string]time.Time),
	}, nil
}

func splitEndpoints(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (c *Client) withAuth(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		"authorization", "Bearer "+c.cfg.APIKey,
		"x-api-key", c.cfg.APIKey,
	)
}

func (c *Client) markBad(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.badUntil[addr] = time.Now().Add(unhealthyFor)
	if c.lastOK == addr {
		c.lastOK = ""
	}
}

func (c *Client) markOK(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastOK = addr
	delete(c.badUntil, addr)
}

func (c *Client) pickOrder() []string {
	c.mu.Lock()
	last := c.lastOK
	now := time.Now()
	n := len(c.cfg.Endpoints)
	order := make([]string, 0, n)
	if last != "" {
		if until, bad := c.badUntil[last]; !bad || now.After(until) {
			order = append(order, last)
		}
	}
	start := int(c.rr.Add(1)-1) % n
	for i := 0; i < n; i++ {
		addr := c.cfg.Endpoints[(start+i)%n]
		if addr == last {
			continue
		}
		if until, bad := c.badUntil[addr]; bad && now.Before(until) {
			continue
		}
		order = append(order, addr)
	}
	// If everything is marked bad, try all anyway.
	if len(order) == 0 {
		order = append(order, c.cfg.Endpoints...)
	}
	c.mu.Unlock()
	return order
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return true // dial / transport
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

func (c *Client) dial(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(normalizeAddr(addr), grpc.WithTransportCredentials(credentials.NewTLS(c.tlsCfg)))
}

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, "dns:///") || strings.Contains(addr, "://") {
		return addr
	}
	return "dns:///" + addr
}

func (c *Client) withConn(ctx context.Context, fn func(ctx context.Context, api cellarv1.SandboxAPIClient) error) error {
	ctx = c.withAuth(ctx)
	var lastErr error
	order := c.pickOrder()
	attempts := 0
	for _, addr := range order {
		if attempts >= c.cfg.MaxAttempts {
			break
		}
		attempts++
		conn, err := c.dial(addr)
		if err != nil {
			c.markBad(addr)
			lastErr = err
			continue
		}
		err = fn(ctx, cellarv1.NewSandboxAPIClient(conn))
		_ = conn.Close()
		if err == nil {
			c.markOK(addr)
			return nil
		}
		lastErr = err
		if retryable(err) {
			c.markBad(addr)
			continue
		}
		return err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no endpoints available")
	}
	return lastErr
}

// Create creates a sandbox.
func (c *Client) Create(ctx context.Context, req *cellarv1.SandboxCreateRequest) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := c.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.Create(ctx, req)
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
}

// Stop stops a sandbox.
func (c *Client) Stop(ctx context.Context, id string) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := c.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.Stop(ctx, &cellarv1.SandboxStopRequest{SandboxId: id})
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
}

// Remove deletes a sandbox.
func (c *Client) Remove(ctx context.Context, id string) error {
	return c.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		_, err := api.Remove(ctx, &cellarv1.SandboxRemoveRequest{SandboxId: id})
		return err
	})
}

// Get returns a sandbox.
func (c *Client) Get(ctx context.Context, id string) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := c.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.Get(ctx, &cellarv1.SandboxGetRequest{SandboxId: id})
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
}

// List returns all sandboxes.
func (c *Client) List(ctx context.Context) ([]*cellarv1.Sandbox, error) {
	var out []*cellarv1.Sandbox
	err := c.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.List(ctx, &cellarv1.SandboxListRequest{})
		if err != nil {
			return err
		}
		out = resp.Sandboxes
		return nil
	})
	return out, err
}

// UpdateNetwork replaces a sandbox network policy.
func (c *Client) UpdateNetwork(ctx context.Context, req *cellarv1.SandboxUpdateNetworkRequest) (*cellarv1.Sandbox, error) {
	var out *cellarv1.Sandbox
	err := c.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		resp, err := api.UpdateNetwork(ctx, req)
		if err != nil {
			return err
		}
		out = resp.Sandbox
		return nil
	})
	return out, err
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
	var result *ExecResult
	err := c.withConn(ctx, func(ctx context.Context, api cellarv1.SandboxAPIClient) error {
		stream, err := api.Exec(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(&cellarv1.SandboxExecMessage{
			Payload: &cellarv1.SandboxExecMessage_Start{Start: &cellarv1.SandboxExecStart{
				SandboxId: sandboxID,
				Command:   command,
			}},
		}); err != nil {
			return err
		}
		_ = stream.CloseSend()
		res := &ExecResult{}
		for {
			msg, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			switch p := msg.Payload.(type) {
			case *cellarv1.SandboxExecMessage_Stdout:
				res.Stdout = append(res.Stdout, p.Stdout...)
			case *cellarv1.SandboxExecMessage_Stderr:
				res.Stderr = append(res.Stderr, p.Stderr...)
			case *cellarv1.SandboxExecMessage_Exit:
				res.ExitCode = p.Exit.ExitCode
				res.Error = p.Exit.Error
				result = res
				return nil
			}
		}
		result = res
		return nil
	})
	return result, err
}
