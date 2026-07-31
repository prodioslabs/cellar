package gateway

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

var (
	protoMarshal = protojson.MarshalOptions{
		UseProtoNames:   false, // camelCase JSON
		EmitUnpopulated: false,
	}
	protoUnmarshal = protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
)

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
		c.AbortWithStatusJSON(http.StatusUnauthorized, errorBody{
			Error: "API key required",
			Code:  "unauthenticated",
		})
		return "", false
	}
	return k, true
}

func writeProtoJSON(c *gin.Context, status int, msg proto.Message) {
	b, err := protoMarshal.Marshal(msg)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, errorBody{Error: "encode response"})
		return
	}
	c.Data(status, "application/json; charset=utf-8", b)
}

func readProtoJSON(c *gin.Context, maxBytes int64, msg proto.Message) bool {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBytes+1))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "read body", Code: "invalid_argument"})
		return false
	}
	if int64(len(body)) > maxBytes {
		c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, errorBody{Error: "request body too large", Code: "invalid_argument"})
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := protoUnmarshal.Unmarshal(body, msg); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{
			Error: fmt.Sprintf("invalid JSON: %v", err),
			Code:  "invalid_argument",
		})
		return false
	}
	return true
}

func (s *Server) handleCreate(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	req := &cellarv1.SandboxCreateRequest{}
	if !readProtoJSON(c, s.cfg.MaxBodyBytes, req) {
		return
	}
	sb, err := s.up.Create(c.Request.Context(), apiKey, req)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	writeProtoJSON(c, http.StatusOK, sb)
}

func (s *Server) handleList(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	list, err := s.up.List(c.Request.Context(), apiKey)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	resp := &cellarv1.SandboxListResponse{Sandboxes: list}
	writeProtoJSON(c, http.StatusOK, resp)
}

func (s *Server) handleGet(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	id := c.Param("id")
	sb, err := s.up.Get(c.Request.Context(), apiKey, id)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	writeProtoJSON(c, http.StatusOK, sb)
}

func (s *Server) handleDelete(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	id := c.Param("id")
	if err := s.up.Remove(c.Request.Context(), apiKey, id); err != nil {
		writeGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleStop(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	id := c.Param("id")
	sb, err := s.up.Stop(c.Request.Context(), apiKey, id)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	writeProtoJSON(c, http.StatusOK, sb)
}

func (s *Server) handleUpdateNetwork(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	id := c.Param("id")
	req := &cellarv1.SandboxUpdateNetworkRequest{}
	if !readProtoJSON(c, s.cfg.MaxBodyBytes, req) {
		return
	}
	req.SandboxId = id
	sb, err := s.up.UpdateNetwork(c.Request.Context(), apiKey, req)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	writeProtoJSON(c, http.StatusOK, sb)
}

func (s *Server) handleLogs(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	id := c.Param("id")
	req := &cellarv1.SandboxLogsRequest{
		SandboxId:  id,
		Follow:     queryBool(c, "follow"),
		Timestamps: queryBool(c, "timestamps"),
	}
	if t := c.Query("tail"); t != "" {
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "invalid tail", Code: "invalid_argument"})
			return
		}
		req.Tail = n
	}

	stream, err := s.up.Logs(c.Request.Context(), apiKey, req)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	defer stream.Close()

	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()

	flusher, canFlush := c.Writer.(http.Flusher)
	enc := json.NewEncoder(c.Writer)
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			// Client disconnect or upstream error mid-stream: stop quietly if
			// headers already sent; otherwise surface as gateway error.
			if !c.Writer.Written() {
				writeGRPCError(c, err)
			}
			return
		}
		line := map[string]string{
			"data": base64.StdEncoding.EncodeToString(chunk.GetData()),
		}
		if err := enc.Encode(line); err != nil {
			return
		}
		if canFlush {
			flusher.Flush()
		}
		if c.Request.Context().Err() != nil {
			return
		}
	}
}

type execRequestBody struct {
	Command []string `json:"command"`
	Detach  bool     `json:"detach,omitempty"`
}

type execResponseBody struct {
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int32  `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`
	JobID    string `json:"jobId,omitempty"`
}

func (s *Server) handleExec(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	id := c.Param("id")
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, s.cfg.MaxBodyBytes+1))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "read body", Code: "invalid_argument"})
		return
	}
	if int64(len(body)) > s.cfg.MaxBodyBytes {
		c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, errorBody{Error: "request body too large", Code: "invalid_argument"})
		return
	}
	var req execRequestBody
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "invalid JSON", Code: "invalid_argument"})
			return
		}
	}
	if len(req.Command) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "command is required", Code: "invalid_argument"})
		return
	}
	if req.Detach {
		jobID, err := s.up.StartJob(c.Request.Context(), apiKey, id, req.Command)
		if err != nil {
			writeGRPCError(c, err)
			return
		}
		c.JSON(http.StatusOK, execResponseBody{JobID: jobID})
		return
	}
	res, err := s.up.Exec(c.Request.Context(), apiKey, id, req.Command)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, execResponseBody{
		Stdout:   string(res.Stdout),
		Stderr:   string(res.Stderr),
		ExitCode: res.ExitCode,
		Error:    res.Error,
	})
}

func (s *Server) handleListJobs(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	jobs, err := s.up.ListJobs(c.Request.Context(), apiKey, c.Param("id"))
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"jobs": jobsToJSON(jobs)})
}

func (s *Server) handleGetJob(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	job, err := s.up.GetJob(c.Request.Context(), apiKey, c.Param("id"), c.Param("jobId"))
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, jobToJSON(job))
}

func (s *Server) handleStopJob(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	if err := s.up.StopJob(c.Request.Context(), apiKey, c.Param("id"), c.Param("jobId"), 10); err != nil {
		writeGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleJobLogs(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	stream, err := s.up.JobLogs(c.Request.Context(), apiKey, &cellarv1.JobLogsRequest{
		SandboxId: c.Param("id"),
		JobId:     c.Param("jobId"),
		Follow:    queryBool(c, "follow"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	defer stream.Close()
	c.Header("Content-Type", "application/x-ndjson")
	c.Status(http.StatusOK)
	flusher, canFlush := c.Writer.(http.Flusher)
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
		line, _ := json.Marshal(gin.H{"data": string(chunk.Data)})
		_, _ = c.Writer.Write(append(line, '\n'))
		if canFlush {
			flusher.Flush()
		}
		if c.Request.Context().Err() != nil {
			return
		}
	}
}

func jobsToJSON(jobs []*cellarv1.JobInfo) []gin.H {
	out := make([]gin.H, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobToJSON(j))
	}
	return out
}

func jobToJSON(j *cellarv1.JobInfo) gin.H {
	if j == nil {
		return gin.H{}
	}
	return gin.H{
		"id":        j.Id,
		"command":   j.Command,
		"phase":     j.Phase,
		"exitCode":  j.ExitCode,
		"startedAt": j.StartedAtUnixNano,
	}
}

func (s *Server) handleHealthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) handleReadyz(c *gin.Context) {
	ctx, cancel := contextWithTimeout(c, s.cfg.ReadyTimeout)
	defer cancel()
	if err := s.up.Ready(ctx); err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, errorBody{
			Error: err.Error(),
			Code:  "unavailable",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

func queryBool(c *gin.Context, name string) bool {
	v := strings.ToLower(strings.TrimSpace(c.Query(name)))
	return v == "1" || v == "true" || v == "yes"
}
