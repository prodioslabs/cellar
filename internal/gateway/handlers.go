package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// cloudSandboxResponse is the msb-cloud sandbox JSON shape.
type cloudSandboxResponse struct {
	ID                 string          `json:"id"`
	OrgID              string          `json:"org_id"`
	Name               string          `json:"name"`
	Slug               string          `json:"slug"`
	Status             string          `json:"status"`
	Spec               json.RawMessage `json:"spec,omitempty"`
	Ephemeral          bool            `json:"ephemeral"`
	CreatedAt          string          `json:"created_at"`
	StartedAt          *string         `json:"started_at"`
	StoppedAt          *string         `json:"stopped_at"`
	LastFailureMessage *string         `json:"last_failure_message"`
}

type cloudListResponse struct {
	Data       []cloudSandboxResponse `json:"data"`
	NextCursor *string                 `json:"next_cursor,omitempty"`
}

type messageResponse struct {
	Message string `json:"message"`
}

type logEventData struct {
	Source string `json:"source"`
	Ts     string `json:"ts"`
	Text   string `json:"text"`
}

func extractAPIKey(c *gin.Context) (string, bool) {
	if k := strings.TrimSpace(c.GetHeader("X-Api-Key")); k != "" {
		return k, true
	}
	auth := c.GetHeader("Authorization")
	const bearer = "Bearer "
	if len(auth) > len(bearer) && strings.EqualFold(auth[:len(bearer)], bearer) {
		k := strings.TrimSpace(auth[len(bearer):])
		if k != "" {
			return k, true
		}
	}
	return "", false
}

func requireAPIKey(c *gin.Context) (string, bool) {
	k, ok := extractAPIKey(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "unauthenticated", "API key required")
		return "", false
	}
	return k, true
}

func queryBool(c *gin.Context, name string) bool {
	v := strings.ToLower(strings.TrimSpace(c.Query(name)))
	return v == "1" || v == "true" || v == "yes"
}

func rfc3339Ptr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}

func toCloudSandbox(sb *cellarv1.Sandbox, orgID string) cloudSandboxResponse {
	if sb == nil {
		return cloudSandboxResponse{OrgID: orgID}
	}
	s := sandbox.FromProto(sb)
	out := cloudSandboxResponse{
		ID:        s.ID,
		OrgID:     orgID,
		Name:      s.Name,
		Slug:      s.Slug,
		Status:    string(s.Status.Phase),
		Ephemeral: s.Ephemeral,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339Nano),
		StartedAt: rfc3339Ptr(s.Status.StartedAt),
		StoppedAt: rfc3339Ptr(s.Status.StoppedAt),
	}
	if out.Status == "" {
		out.Status = string(sandbox.PhaseCreated)
	}
	if len(sb.SpecJson) > 0 {
		out.Spec = json.RawMessage(sb.SpecJson)
	} else if specJSON, err := sandbox.SpecToJSON(s.Spec); err == nil {
		out.Spec = json.RawMessage(specJSON)
	}
	if s.Status.Phase == sandbox.PhaseFailed && s.Status.Message != "" {
		msg := s.Status.Message
		out.LastFailureMessage = &msg
	}
	return out
}

func (s *Server) orgID() string {
	if id := s.up.ClusterID(); id != "" {
		return id
	}
	return ""
}

func (s *Server) writeSandbox(c *gin.Context, status int, sb *cellarv1.Sandbox) {
	c.JSON(status, toCloudSandbox(sb, s.orgID()))
}

func (s *Server) waitForRunning(ctx context.Context, apiKey, id string, timeout time.Duration) (*cellarv1.Sandbox, error) {
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	deadline := time.Now().Add(timeout)
	var last *cellarv1.Sandbox
	for {
		sb, err := s.up.Get(ctx, apiKey, id)
		if err != nil {
			return nil, err
		}
		last = sb
		phase := ""
		if sb != nil && sb.Status != nil {
			phase = sb.Status.Phase
		}
		switch sandbox.StatusPhase(phase) {
		case sandbox.PhaseRunning:
			return sb, nil
		case sandbox.PhaseFailed:
			msg := "sandbox failed"
			if sb.Status != nil && sb.Status.Message != "" {
				msg = sb.Status.Message
			}
			return sb, status.Error(codes.FailedPrecondition, msg)
		case sandbox.PhaseStopped, sandbox.PhaseStopping:
			return sb, status.Error(codes.FailedPrecondition, "sandbox stopped before reaching running")
		}
		if time.Now().After(deadline) {
			return last, status.Error(codes.DeadlineExceeded, "timed out waiting for sandbox to become running")
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func parseWaitTimeout(c *gin.Context) time.Duration {
	raw := strings.TrimSpace(c.Query("wait_timeout"))
	if raw == "" {
		return DefaultWaitTimeout
	}
	secs, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return DefaultWaitTimeout
	}
	return time.Duration(secs) * time.Second
}

func (s *Server) handleHealthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) handleReadyz(c *gin.Context) {
	ctx, cancel := contextWithTimeout(c, s.cfg.ReadyTimeout)
	defer cancel()
	if err := s.up.Ready(ctx); err != nil {
		writeError(c, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

func (s *Server) handleCreateSandbox(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, s.cfg.MaxBodyBytes+1))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "read body")
		return
	}
	if int64(len(body)) > s.cfg.MaxBodyBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "invalid_request", "request body too large")
		return
	}
	if len(body) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "request body required")
		return
	}
	spec, err := sandbox.SpecFromJSON(body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_sandbox_config", err.Error())
		return
	}
	if err := sandbox.ValidateSpec(spec); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_sandbox_config", err.Error())
		return
	}

	start := queryBool(c, "start")
	waitFor := strings.TrimSpace(c.Query("wait_for"))
	sb, err := s.up.Create(c.Request.Context(), apiKey, body, start)
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	if waitFor == "running" && sb != nil {
		timeout := parseWaitTimeout(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout+time.Second)
		defer cancel()
		sb, err = s.waitForRunning(ctx, apiKey, sb.Id, timeout)
		if err != nil {
			writeGRPCErrorKind(c, err, "sandbox")
			return
		}
	}
	s.writeSandbox(c, http.StatusOK, sb)
}

func (s *Server) handleListSandboxes(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var limit uint32
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid limit")
			return
		}
		limit = uint32(n)
	}
	list, next, err := s.up.List(c.Request.Context(), apiKey, c.Query("cursor"), limit, c.Query("labels"))
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	org := s.orgID()
	data := make([]cloudSandboxResponse, 0, len(list))
	for _, sb := range list {
		data = append(data, toCloudSandbox(sb, org))
	}
	resp := cloudListResponse{Data: data}
	if next != "" {
		resp.NextCursor = &next
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleGetSandbox(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	sb, err := s.up.Get(c.Request.Context(), apiKey, c.Param("id"))
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	s.writeSandbox(c, http.StatusOK, sb)
}

func (s *Server) handleGetSandboxByName(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	sb, err := s.up.GetByName(c.Request.Context(), apiKey, c.Param("name"))
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	s.writeSandbox(c, http.StatusOK, sb)
}

func (s *Server) handleStartSandbox(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	id := c.Param("id")
	sb, err := s.up.Start(c.Request.Context(), apiKey, id)
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	if strings.TrimSpace(c.Query("wait_for")) == "running" {
		timeout := parseWaitTimeout(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout+time.Second)
		defer cancel()
		sb, err = s.waitForRunning(ctx, apiKey, id, timeout)
		if err != nil {
			writeGRPCErrorKind(c, err, "sandbox")
			return
		}
	}
	s.writeSandbox(c, http.StatusOK, sb)
}

func (s *Server) handleStartSandboxByName(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	sb, err := s.up.GetByName(c.Request.Context(), apiKey, c.Param("name"))
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	sb, err = s.up.Start(c.Request.Context(), apiKey, sb.Id)
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	if strings.TrimSpace(c.Query("wait_for")) == "running" {
		timeout := parseWaitTimeout(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout+time.Second)
		defer cancel()
		sb, err = s.waitForRunning(ctx, apiKey, sb.Id, timeout)
		if err != nil {
			writeGRPCErrorKind(c, err, "sandbox")
			return
		}
	}
	s.writeSandbox(c, http.StatusOK, sb)
}

func (s *Server) handleStopSandbox(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	sb, err := s.up.Stop(c.Request.Context(), apiKey, c.Param("id"))
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	s.writeSandbox(c, http.StatusOK, sb)
}

func (s *Server) handleStopSandboxByName(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	sb, err := s.up.GetByName(c.Request.Context(), apiKey, c.Param("name"))
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	sb, err = s.up.Stop(c.Request.Context(), apiKey, sb.Id)
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	s.writeSandbox(c, http.StatusOK, sb)
}

func (s *Server) handleDeleteSandbox(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	if err := s.up.Remove(c.Request.Context(), apiKey, c.Param("id")); err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	c.JSON(http.StatusOK, messageResponse{Message: "sandbox deleted"})
}

func (s *Server) handleDeleteSandboxByName(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	sb, err := s.up.GetByName(c.Request.Context(), apiKey, c.Param("name"))
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	if err := s.up.Remove(c.Request.Context(), apiKey, sb.Id); err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	c.JSON(http.StatusOK, messageResponse{Message: "sandbox deleted"})
}

func (s *Server) handleSandboxLogs(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	req := &cellarv1.SandboxLogsRequest{
		SandboxId:   c.Param("id"),
		Follow:      true,
		Sources:     c.Query("sources"),
		LastEventId: c.GetHeader("Last-Event-ID"),
	}
	stream, err := s.up.Logs(c.Request.Context(), apiKey, req)
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	defer stream.Close()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
	flusher, canFlush := c.Writer.(http.Flusher)

	writeSSE := func(event, id string, data any) bool {
		payload, err := json.Marshal(data)
		if err != nil {
			return false
		}
		if id != "" {
			if _, err := c.Writer.Write([]byte("id: " + id + "\n")); err != nil {
				return false
			}
		}
		if _, err := c.Writer.Write([]byte("event: " + event + "\n")); err != nil {
			return false
		}
		if _, err := c.Writer.Write([]byte("data: ")); err != nil {
			return false
		}
		if _, err := c.Writer.Write(payload); err != nil {
			return false
		}
		if _, err := c.Writer.Write([]byte("\n\n")); err != nil {
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			_ = writeSSE("end", "", gin.H{})
			return
		}
		if err != nil {
			return
		}
		ts := time.Unix(0, chunk.GetTsUnixNano()).UTC().Format(time.RFC3339Nano)
		if !writeSSE("log", chunk.GetId(), logEventData{
			Source: chunk.GetSource(),
			Ts:     ts,
			Text:   chunk.GetText(),
		}) {
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}
	}
}

func (s *Server) handleSandboxAgent(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	relay, err := s.up.AgentRelay(c.Request.Context(), apiKey, c.Param("id"))
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	defer relay.Close()

	ws, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	errCh := make(chan error, 2)
	go func() {
		for {
			data, err := relay.Recv()
			if err != nil {
				errCh <- err
				return
			}
			if len(data) == 0 {
				continue
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if mt != websocket.BinaryMessage && mt != websocket.TextMessage {
				continue
			}
			if len(data) == 0 {
				continue
			}
			if err := relay.Send(data); err != nil {
				errCh <- err
				return
			}
		}
	}()
	<-errCh
}
