package gateway

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Server is the Gin HTTP gateway.
type Server struct {
	cfg Config
	up  Upstream
	eng *gin.Engine
}

// New builds a Server. up may be nil to construct a GRPCUpstream from cfg.
func New(cfg Config, up Upstream) (*Server, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if up == nil {
		up = &GRPCUpstream{
			Resolver: &DataDirResolver{
				DataDir:   cfg.DataDir,
				Overrides: cfg.Upstreams,
			},
		}
	}
	gin.SetMode(gin.ReleaseMode)
	eng := gin.New()
	eng.Use(gin.Recovery())
	eng.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{"/healthz", "/readyz"},
	}))
	eng.MaxMultipartMemory = cfg.MaxBodyBytes

	s := &Server{cfg: cfg, up: up, eng: eng}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.eng.GET("/healthz", s.handleHealthz)
	s.eng.GET("/readyz", s.handleReadyz)

	v1 := s.eng.Group("/v1")
	{
		v1.POST("/sandboxes", s.handleCreate)
		v1.GET("/sandboxes", s.handleList)
		v1.GET("/sandboxes/:id", s.handleGet)
		v1.DELETE("/sandboxes/:id", s.handleDelete)
		v1.POST("/sandboxes/:id/stop", s.handleStop)
		v1.PUT("/sandboxes/:id/network", s.handleUpdateNetwork)
		v1.GET("/sandboxes/:id/logs", s.handleLogs)
		v1.POST("/sandboxes/:id/exec", s.handleExec)
		v1.GET("/sandboxes/:id/jobs", s.handleListJobs)
		v1.GET("/sandboxes/:id/jobs/:jobId", s.handleGetJob)
		v1.DELETE("/sandboxes/:id/jobs/:jobId", s.handleStopJob)
		v1.GET("/sandboxes/:id/jobs/:jobId/logs", s.handleJobLogs)
		v1.GET("/sandboxes/:id/fs/content", s.handleFsGetContent)
		v1.PUT("/sandboxes/:id/fs/content", s.handleFsPutContent)
		v1.GET("/sandboxes/:id/fs/stat", s.handleFsStat)
		v1.GET("/sandboxes/:id/fs/list", s.handleFsList)
		v1.GET("/sandboxes/:id/fs/exists", s.handleFsExists)
		v1.POST("/sandboxes/:id/fs/mkdir", s.handleFsMkdir)
		v1.POST("/sandboxes/:id/fs/remove", s.handleFsRemove)
		v1.POST("/sandboxes/:id/fs/remove-dir", s.handleFsRemoveDir)
		v1.POST("/sandboxes/:id/fs/copy", s.handleFsCopy)
		v1.POST("/sandboxes/:id/fs/rename", s.handleFsRename)
	}
}

// Handler returns the HTTP handler for tests.
func (s *Server) Handler() http.Handler {
	return s.eng
}

// Run listens on cfg.ListenAddr until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.ListenAddr, err)
	}
	srv := &http.Server{
		Handler:           s.eng,
		ReadHeaderTimeout: 10 * time.Second,
		// ReadTimeout unset (0) so large fs PUT bodies and long uploads are not
		// cut by a fixed deadline; headers still capped by ReadHeaderTimeout.
		ReadTimeout: 0,
		// IdleTimeout leaves room for long-lived log streams behind an ALB.
		IdleTimeout: 120 * time.Second,
		// WriteTimeout is unset (0) so streaming logs/exec can run longer than
		// a fixed write deadline; callers should cancel via context.
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("cellar-gateway listening on %s", ln.Addr())
		errCh <- srv.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		err := <-errCh
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func contextWithTimeout(c *gin.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), d)
}
