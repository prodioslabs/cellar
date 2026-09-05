package gateway

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

const (
	headerFilePath      = "x-msb-file-path"
	headerFileRecursive = "x-msb-file-recursive"
)

type cloudVolumeResponse struct {
	ID            string            `json:"id"`
	Name          *string           `json:"name,omitempty"`
	Kind          string            `json:"kind"`
	Status        string            `json:"status"`
	UsedBytes     *uint64           `json:"used_bytes,omitempty"`
	CapacityBytes *uint64           `json:"capacity_bytes,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
}

type volumeCreateBody struct {
	Name        string            `json:"name"`
	CapacityGiB *uint32           `json:"capacity_gib,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type fsPathBody struct {
	Path string `json:"path"`
}

type fsCopyBody struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type cloudFileInfo struct {
	Path       string  `json:"path"`
	Kind       string  `json:"kind"`
	Size       int64   `json:"size"`
	Mode       uint32  `json:"mode"`
	UID        uint32  `json:"uid"`
	GID        uint32  `json:"gid"`
	Readonly   bool    `json:"readonly"`
	ModifiedAt *string `json:"modified_at,omitempty"`
	CreatedAt  *string `json:"created_at,omitempty"`
}

func toCloudVolume(p *cellarv1.Volume) cloudVolumeResponse {
	if p == nil {
		return cloudVolumeResponse{}
	}
	v := sandbox.VolumeFromProto(p)
	out := cloudVolumeResponse{
		ID:            v.ID,
		Kind:          string(v.Kind),
		Status:        string(v.Status),
		UsedBytes:     v.UsedBytes,
		CapacityBytes: v.CapacityBytes,
		Labels:        v.Labels,
		CreatedAt:     v.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     v.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if v.Name != "" {
		name := v.Name
		out.Name = &name
	}
	return out
}

func decodeMSBPath(c *gin.Context) (string, bool) {
	raw := strings.TrimSpace(c.GetHeader(headerFilePath))
	if raw == "" {
		writeError(c, http.StatusBadRequest, "invalid_request", "x-msb-file-path header required")
		return "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		// Accept padded base64url as well.
		b, err = base64.URLEncoding.DecodeString(raw)
	}
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_volume_path", "invalid x-msb-file-path encoding")
		return "", false
	}
	path := string(b)
	if path == "" {
		writeError(c, http.StatusBadRequest, "invalid_volume_path", "path is empty")
		return "", false
	}
	return path, true
}

func recursiveHeader(c *gin.Context) bool {
	v := strings.ToLower(strings.TrimSpace(c.GetHeader(headerFileRecursive)))
	return v == "1" || v == "true" || v == "yes"
}

func readJSONBody(c *gin.Context, max int64, out any) bool {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, max+1))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "read body")
		return false
	}
	if int64(len(body)) > max {
		writeError(c, http.StatusRequestEntityTooLarge, "invalid_request", "request body too large")
		return false
	}
	if len(body) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return false
	}
	if err := json.Unmarshal(body, out); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return false
	}
	return true
}

func fsTimePtr(nanos int64) *string {
	if nanos == 0 {
		return nil
	}
	s := time.Unix(0, nanos).UTC().Format(time.RFC3339Nano)
	return &s
}

func metaToCloud(m *cellarv1.FsMetadata) cloudFileInfo {
	if m == nil {
		return cloudFileInfo{}
	}
	return cloudFileInfo{
		Path:       m.Path,
		Kind:       m.Kind,
		Size:       m.Size,
		Mode:       m.Mode,
		UID:        m.Uid,
		GID:        m.Gid,
		Readonly:   m.Readonly,
		ModifiedAt: fsTimePtr(m.ModifiedUnixNano),
		CreatedAt:  fsTimePtr(m.CreatedUnixNano),
	}
}

func entryToCloud(e *cellarv1.FsEntry) cloudFileInfo {
	if e == nil {
		return cloudFileInfo{}
	}
	return cloudFileInfo{
		Path:       e.Path,
		Kind:       e.Kind,
		Size:       e.Size,
		Mode:       e.Mode,
		ModifiedAt: fsTimePtr(e.ModifiedUnixNano),
	}
}

func (s *Server) handleListVolumes(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	vols, err := s.up.ListVolumes(c.Request.Context(), apiKey)
	if err != nil {
		writeGRPCErrorKind(c, err, "volume")
		return
	}
	out := make([]cloudVolumeResponse, 0, len(vols))
	for _, v := range vols {
		out = append(out, toCloudVolume(v))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleCreateVolume(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var body volumeCreateBody
	if !readJSONBody(c, s.cfg.MaxBodyBytes, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(c, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	req := &cellarv1.VolumeCreateRequest{Name: body.Name, CapacityGib: body.CapacityGiB}
	if len(body.Labels) > 0 {
		req.LabelsJson, _ = json.Marshal(body.Labels)
	}
	vol, err := s.up.CreateVolume(c.Request.Context(), apiKey, req)
	if err != nil {
		writeGRPCErrorKind(c, err, "volume")
		return
	}
	c.JSON(http.StatusOK, toCloudVolume(vol))
}

func (s *Server) handleGetDefaultVolume(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	vol, err := s.up.GetDefaultVolume(c.Request.Context(), apiKey)
	if err != nil {
		writeGRPCErrorKind(c, err, "volume")
		return
	}
	c.JSON(http.StatusOK, toCloudVolume(vol))
}

func (s *Server) handleDeleteVolume(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	msg, err := s.up.DeleteVolume(c.Request.Context(), apiKey, c.Param("id"))
	if err != nil {
		writeGRPCErrorKind(c, err, "volume")
		return
	}
	if msg == "" {
		msg = "volume deleted"
	}
	c.JSON(http.StatusOK, messageResponse{Message: msg})
}

func (s *Server) handleVolumeFsGetContent(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	path, ok := decodeMSBPath(c)
	if !ok {
		return
	}
	stream, err := s.up.VolumeFsRead(c.Request.Context(), apiKey, c.Param("id"), path)
	if err != nil {
		writeGRPCErrorKind(c, err, "volume_file")
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

func (s *Server) handleVolumeFsPutContent(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	path, ok := decodeMSBPath(c)
	if !ok {
		return
	}
	if err := s.up.VolumeFsWrite(c.Request.Context(), apiKey, c.Param("id"), path, c.Request.Body); err != nil {
		writeGRPCErrorKind(c, err, "volume_file")
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleVolumeFsList(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	path, ok := decodeMSBPath(c)
	if !ok {
		return
	}
	entries, err := s.up.VolumeFsList(c.Request.Context(), apiKey, c.Param("id"), path)
	if err != nil {
		writeGRPCErrorKind(c, err, "volume_file")
		return
	}
	out := make([]cloudFileInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, entryToCloud(e))
	}
	c.JSON(http.StatusOK, gin.H{"entries": out})
}

func (s *Server) handleVolumeFsStat(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	path, ok := decodeMSBPath(c)
	if !ok {
		return
	}
	meta, err := s.up.VolumeFsStat(c.Request.Context(), apiKey, c.Param("id"), path)
	if err != nil {
		writeGRPCErrorKind(c, err, "volume_file")
		return
	}
	c.JSON(http.StatusOK, gin.H{"metadata": metaToCloud(meta)})
}

func (s *Server) handleVolumeFsExists(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	path, ok := decodeMSBPath(c)
	if !ok {
		return
	}
	exists, err := s.up.VolumeFsExists(c.Request.Context(), apiKey, c.Param("id"), path)
	if err != nil {
		writeGRPCErrorKind(c, err, "volume_file")
		return
	}
	c.JSON(http.StatusOK, gin.H{"exists": exists})
}

func (s *Server) handleVolumeFsMkdir(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var body fsPathBody
	if !readJSONBody(c, s.cfg.MaxBodyBytes, &body) {
		return
	}
	if body.Path == "" {
		writeError(c, http.StatusBadRequest, "invalid_request", "path is required")
		return
	}
	if err := s.up.VolumeFsMkdir(c.Request.Context(), apiKey, c.Param("id"), body.Path); err != nil {
		writeGRPCErrorKind(c, err, "volume_file")
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleVolumeFsRemove(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	path, ok := decodeMSBPath(c)
	if !ok {
		return
	}
	if err := s.up.VolumeFsRemove(c.Request.Context(), apiKey, c.Param("id"), path, recursiveHeader(c)); err != nil {
		writeGRPCErrorKind(c, err, "volume_file")
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleVolumeFsCopy(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var body fsCopyBody
	if !readJSONBody(c, s.cfg.MaxBodyBytes, &body) {
		return
	}
	if body.From == "" || body.To == "" {
		writeError(c, http.StatusBadRequest, "invalid_request", "from and to are required")
		return
	}
	if err := s.up.VolumeFsCopy(c.Request.Context(), apiKey, c.Param("id"), body.From, body.To); err != nil {
		writeGRPCErrorKind(c, err, "volume_file")
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleVolumeFsRename(c *gin.Context) {
	apiKey, ok := requireAPIKey(c)
	if !ok {
		return
	}
	var body fsCopyBody
	if !readJSONBody(c, s.cfg.MaxBodyBytes, &body) {
		return
	}
	if body.From == "" || body.To == "" {
		writeError(c, http.StatusBadRequest, "invalid_request", "from and to are required")
		return
	}
	if err := s.up.VolumeFsRename(c.Request.Context(), apiKey, c.Param("id"), body.From, body.To); err != nil {
		writeGRPCErrorKind(c, err, "volume_file")
		return
	}
	c.Status(http.StatusNoContent)
}
