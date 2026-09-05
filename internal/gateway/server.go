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
		resolver := &DataDirResolver{
			DataDir:    cfg.DataDir,
			SocketPath: cfg.SocketPath,
			Overrides:  cfg.Upstreams,
		}
		up = &GRPCUpstream{
			Resolver: resolver,
			Identity: resolver,
			Runtime:  resolver,
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
		v1.POST("/sandboxes", s.handleCreateSandbox)
		v1.GET("/sandboxes", s.handleListSandboxes)

		v1.GET("/sandboxes/by-name/:name", s.handleGetSandboxByName)
		v1.POST("/sandboxes/by-name/:name/start", s.handleStartSandboxByName)
		v1.POST("/sandboxes/by-name/:name/stop", s.handleStopSandboxByName)
		v1.DELETE("/sandboxes/by-name/:name", s.handleDeleteSandboxByName)

		v1.GET("/sandboxes/:id", s.handleGetSandbox)
		v1.POST("/sandboxes/:id/start", s.handleStartSandbox)
		v1.POST("/sandboxes/:id/stop", s.handleStopSandbox)
		v1.DELETE("/sandboxes/:id", s.handleDeleteSandbox)
		v1.GET("/sandboxes/:id/logs", s.handleSandboxLogs)
		v1.GET("/sandboxes/:id/agent", s.handleSandboxAgent)

		v1.GET("/volumes", s.handleListVolumes)
		v1.POST("/volumes", s.handleCreateVolume)
		v1.GET("/volumes/default", s.handleGetDefaultVolume)
		v1.DELETE("/volumes/:id", s.handleDeleteVolume)

		v1.GET("/volumes/:id/files/content", s.handleVolumeFsGetContent)
		v1.PUT("/volumes/:id/files/content", s.handleVolumeFsPutContent)
		v1.GET("/volumes/:id/files/stat", s.handleVolumeFsStat)
		v1.GET("/volumes/:id/files/exists", s.handleVolumeFsExists)
		v1.POST("/volumes/:id/files/mkdir", s.handleVolumeFsMkdir)
		v1.POST("/volumes/:id/files/copy", s.handleVolumeFsCopy)
		v1.POST("/volumes/:id/files/rename", s.handleVolumeFsRename)
		v1.GET("/volumes/:id/files", s.handleVolumeFsList)
		v1.DELETE("/volumes/:id/files", s.handleVolumeFsRemove)
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
		ReadTimeout:       0,
		IdleTimeout:       120 * time.Second,
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
