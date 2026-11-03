package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

type snapshotSource struct {
	Type       string `json:"type"`
	SnapshotID string `json:"snapshot_id"`
}

type createSandboxEnvelope struct {
	Source *snapshotSource `json:"source"`
}

type createSnapshotBody struct {
	Stop       *bool  `json:"stop"`
	Expiration string `json:"expiration"`
}

type patchSandboxBody struct {
	SnapshotExpiration string  `json:"snapshot_expiration"`
	KeepLastSnapshots  *uint32 `json:"keep_last_snapshots"`
	KeepLastExpiration string  `json:"keep_last_expiration"`
	DeleteEvicted      *bool   `json:"delete_evicted"`
}

type snapshotJSON struct {
	ID           string `json:"id"`
	SandboxID    string `json:"sandbox_id"`
	SandboxName  string `json:"sandbox_name"`
	NodeID       string `json:"node_id,omitempty"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	Backend      string `json:"backend"`
	Location     string `json:"location"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	Digest       string `json:"digest,omitempty"`
	ExpirationMs int64  `json:"expiration_ms"`
	LastUsedAt   string `json:"last_used_at,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	Message      string `json:"message,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

func toSnapshotJSON(p *cellarv1.FSSnapshot) snapshotJSON {
	s := sandbox.FSSnapshotFromProto(p)
	out := snapshotJSON{
		ID:           s.ID,
		SandboxID:    s.SandboxID,
		SandboxName:  s.SandboxName,
		NodeID:       s.NodeID,
		Kind:         string(s.Kind),
		Status:       string(s.Status),
		Backend:      string(s.Backend),
		Location:     s.Location,
		SizeBytes:    s.SizeBytes,
		Digest:       s.Digest,
		ExpirationMs: s.ExpirationMs,
		Message:      s.Message,
	}
	if !s.LastUsedAt.IsZero() {
		out.LastUsedAt = s.LastUsedAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
	}
	if !s.ExpiresAt.IsZero() {
		out.ExpiresAt = s.ExpiresAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
	}
	if !s.CreatedAt.IsZero() {
		out.CreatedAt = s.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
	}
	return out
}

func extractSnapshotSource(body []byte) string {
	var env createSandboxEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Source == nil {
		return ""
	}
	if env.Source.Type != "" && env.Source.Type != "snapshot" {
		return ""
	}
	return strings.TrimSpace(env.Source.SnapshotID)
}

func (s *Server) handleCreateSnapshot(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, s.cfg.MaxBodyBytes+1))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "read body")
		return
	}
	var req createSnapshotBody
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	if req.Stop == nil || !*req.Stop {
		writeError(c, http.StatusBadRequest, "invalid_request", "stop=true is required")
		return
	}
	rpc := &cellarv1.SnapshotCreateRequest{SandboxId: c.Param("id"), Stop: true}
	if strings.TrimSpace(req.Expiration) != "" {
		ms, err := sandbox.ParseExpirationMs(req.Expiration)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		rpc.ExpirationMs = &ms
	}
	snap, err := s.up.CreateSnapshot(c.Request.Context(), apiKey, rpc)
	if err != nil {
		writeGRPCErrorKind(c, err, "snapshot")
		return
	}
	c.JSON(http.StatusOK, toSnapshotJSON(snap))
}

func (s *Server) handleListSandboxSnapshots(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	list, err := s.up.ListSnapshots(c.Request.Context(), apiKey, c.Param("id"), "")
	if err != nil {
		writeGRPCErrorKind(c, err, "snapshot")
		return
	}
	out := make([]snapshotJSON, 0, len(list))
	for _, snap := range list {
		out = append(out, toSnapshotJSON(snap))
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

func (s *Server) handleGetSnapshot(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	snap, err := s.up.GetSnapshot(c.Request.Context(), apiKey, c.Param("id"))
	if err != nil {
		writeGRPCErrorKind(c, err, "snapshot")
		return
	}
	c.JSON(http.StatusOK, toSnapshotJSON(snap))
}

func (s *Server) handleDeleteSnapshot(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	if err := s.up.DeleteSnapshot(c.Request.Context(), apiKey, c.Param("id")); err != nil {
		writeGRPCErrorKind(c, err, "snapshot")
		return
	}
	c.JSON(http.StatusOK, messageResponse{Message: "snapshot deleted"})
}

func (s *Server) handlePatchSandbox(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, s.cfg.MaxBodyBytes+1))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "read body")
		return
	}
	var req patchSandboxBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rpc := &cellarv1.SandboxUpdateRequest{SandboxId: c.Param("id")}
	if strings.TrimSpace(req.SnapshotExpiration) != "" {
		ms, err := sandbox.ParseExpirationMs(req.SnapshotExpiration)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		rpc.SnapshotExpirationMs = &ms
	}
	rpc.KeepLastSnapshots = req.KeepLastSnapshots
	if strings.TrimSpace(req.KeepLastExpiration) != "" {
		ms, err := sandbox.ParseExpirationMs(req.KeepLastExpiration)
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		rpc.KeepLastExpirationMs = &ms
	}
	rpc.KeepLastDeleteEvicted = req.DeleteEvicted
	sb, err := s.up.UpdateSandbox(c.Request.Context(), apiKey, rpc)
	if err != nil {
		writeGRPCErrorKind(c, err, "sandbox")
		return
	}
	s.writeSandbox(c, http.StatusOK, sb)
}
