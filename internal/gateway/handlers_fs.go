package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

func (s *Server) requireFSPath(c *gin.Context) (apiKey, path string, ok bool) {
	apiKey, ok = requireAPIKey(c)
	if !ok {
		return "", "", false
	}
	path = c.Query("path")
	if path == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "path query required", Code: "invalid_argument"})
		return "", "", false
	}
	return apiKey, path, true
}

func (s *Server) handleFsGetContent(c *gin.Context) {
	apiKey, path, ok := s.requireFSPath(c)
	if !ok {
		return
	}
	stream, err := s.up.FsRead(c.Request.Context(), apiKey, c.Param("id"), path)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	defer stream.Close()
	c.Header("Content-Type", "application/octet-stream")
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
		if len(chunk.Data) == 0 {
			continue
		}
		_, _ = c.Writer.Write(chunk.Data)
		if canFlush {
			flusher.Flush()
		}
		if c.Request.Context().Err() != nil {
			return
		}
	}
}

func (s *Server) handleFsPutContent(c *gin.Context) {
	apiKey, path, ok := s.requireFSPath(c)
	if !ok {
		return
	}
	if err := s.up.FsWrite(c.Request.Context(), apiKey, c.Param("id"), path, c.Request.Body); err != nil {
		writeGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleFsStat(c *gin.Context) {
	apiKey, path, ok := s.requireFSPath(c)
	if !ok {
		return
	}
	meta, err := s.up.FsStat(c.Request.Context(), apiKey, c.Param("id"), path)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, fsMetadataJSON(meta))
}

func (s *Server) handleFsList(c *gin.Context) {
	apiKey, path, ok := s.requireFSPath(c)
	if !ok {
		return
	}
	entries, err := s.up.FsList(c.Request.Context(), apiKey, c.Param("id"), path)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(entries))
	for _, e := range entries {
		out = append(out, fsEntryJSON(e))
	}
	c.JSON(http.StatusOK, gin.H{"entries": out})
}

func (s *Server) handleFsExists(c *gin.Context) {
	apiKey, path, ok := s.requireFSPath(c)
	if !ok {
		return
	}
	exists, err := s.up.FsExists(c.Request.Context(), apiKey, c.Param("id"), path)
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"exists": exists})
}

type fsPathBody struct {
	Path string `json:"path"`
}

type fsCopyBody struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleFsMkdir(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var body fsPathBody
	if !readJSON(c, s.cfg.MaxBodyBytes, &body) {
		return
	}
	if body.Path == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "path is required", Code: "invalid_argument"})
		return
	}
	if err := s.up.FsMkdir(c.Request.Context(), apiKey, c.Param("id"), body.Path); err != nil {
		writeGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleFsRemove(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var body fsPathBody
	if !readJSON(c, s.cfg.MaxBodyBytes, &body) {
		return
	}
	if body.Path == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "path is required", Code: "invalid_argument"})
		return
	}
	if err := s.up.FsRemove(c.Request.Context(), apiKey, c.Param("id"), body.Path); err != nil {
		writeGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleFsRemoveDir(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var body fsPathBody
	if !readJSON(c, s.cfg.MaxBodyBytes, &body) {
		return
	}
	if body.Path == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "path is required", Code: "invalid_argument"})
		return
	}
	if err := s.up.FsRemoveDir(c.Request.Context(), apiKey, c.Param("id"), body.Path); err != nil {
		writeGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleFsCopy(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var body fsCopyBody
	if !readJSON(c, s.cfg.MaxBodyBytes, &body) {
		return
	}
	if body.From == "" || body.To == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "from and to are required", Code: "invalid_argument"})
		return
	}
	if err := s.up.FsCopy(c.Request.Context(), apiKey, c.Param("id"), body.From, body.To); err != nil {
		writeGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleFsRename(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var body fsCopyBody
	if !readJSON(c, s.cfg.MaxBodyBytes, &body) {
		return
	}
	if body.From == "" || body.To == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "from and to are required", Code: "invalid_argument"})
		return
	}
	if err := s.up.FsRename(c.Request.Context(), apiKey, c.Param("id"), body.From, body.To); err != nil {
		writeGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func readJSON(c *gin.Context, max int64, out any) bool {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, max+1))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "read body", Code: "invalid_argument"})
		return false
	}
	if int64(len(body)) > max {
		c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, errorBody{Error: "request body too large", Code: "invalid_argument"})
		return false
	}
	if len(body) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "invalid JSON", Code: "invalid_argument"})
		return false
	}
	if err := json.Unmarshal(body, out); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, errorBody{Error: "invalid JSON", Code: "invalid_argument"})
		return false
	}
	return true
}

func fsTimeJSON(nanos int64) any {
	if nanos == 0 {
		return nil
	}
	return time.Unix(0, nanos).UTC().Format(time.RFC3339Nano)
}

func fsMetadataJSON(m *cellarv1.FsMetadata) gin.H {
	if m == nil {
		return gin.H{}
	}
	return gin.H{
		"kind":     m.Kind,
		"size":     m.Size,
		"mode":     m.Mode,
		"readonly": m.Readonly,
		"modified": fsTimeJSON(m.ModifiedUnixNano),
		"created":  fsTimeJSON(m.CreatedUnixNano),
	}
}

func fsEntryJSON(e *cellarv1.FsEntry) gin.H {
	if e == nil {
		return gin.H{}
	}
	return gin.H{
		"path":     e.Path,
		"kind":     e.Kind,
		"size":     e.Size,
		"mode":     e.Mode,
		"modified": fsTimeJSON(e.ModifiedUnixNano),
	}
}
