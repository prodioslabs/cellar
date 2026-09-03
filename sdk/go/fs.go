package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// FsEntryKind matches gateway kind strings.
type FsEntryKind string

const (
	FsKindFile      FsEntryKind = "file"
	FsKindDirectory FsEntryKind = "directory"
	FsKindSymlink   FsEntryKind = "symlink"
	FsKindOther     FsEntryKind = "other"
)

// FsEntry is a directory listing entry.
type FsEntry struct {
	Path     string
	Kind     FsEntryKind
	Size     int64
	Mode     uint32
	Modified *time.Time
}

// FsMetadata is detailed file metadata.
type FsMetadata struct {
	Kind     FsEntryKind
	Size     int64
	Mode     uint32
	Readonly bool
	Modified *time.Time
	Created  *time.Time
}

// SandboxFS exposes in-sandbox filesystem operations for a Sandbox.
type SandboxFS struct {
	client    *Client
	sandboxID string
}

// FS returns the filesystem handle for this sandbox.
func (s *Sandbox) FS() *SandboxFS {
	return &SandboxFS{client: s.client, sandboxID: s.data.ID}
}

func (f *SandboxFS) contentURL(path string) string {
	q := url.Values{"path": {path}}
	return f.client.base + "/v1/sandboxes/" + url.PathEscape(f.sandboxID) + "/fs/content?" + q.Encode()
}

func (f *SandboxFS) opURL(op, path string) string {
	q := url.Values{"path": {path}}
	return "/v1/sandboxes/" + url.PathEscape(f.sandboxID) + "/fs/" + op + "?" + q.Encode()
}

// Read reads the entire file into memory.
func (f *SandboxFS) Read(ctx context.Context, path string) ([]byte, error) {
	rc, err := f.ReadStream(ctx, path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// ReadToString reads the entire file and decodes it as UTF-8.
func (f *SandboxFS) ReadToString(ctx context.Context, path string) (string, error) {
	b, err := f.Read(ctx, path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Write writes data to a file (create or overwrite). Parents must exist.
func (f *SandboxFS) Write(ctx context.Context, path string, data []byte) error {
	return f.WriteStream(ctx, path, bytes.NewReader(data))
}

// ReadStream opens a streaming reader for a guest file.
func (f *SandboxFS) ReadStream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.contentURL(path), nil)
	if err != nil {
		return nil, err
	}
	f.client.authHeaders(req.Header)
	req.Header.Set("Accept", "application/octet-stream")
	hc := f.client.http
	// Streaming reads should not inherit a short client timeout.
	if hc.Timeout > 0 {
		clone := *hc
		clone.Timeout = 0
		hc = &clone
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, parseAPIError(resp.StatusCode, data)
	}
	return resp.Body, nil
}

// WriteStream streams r into a guest file (create or overwrite).
func (f *SandboxFS) WriteStream(ctx context.Context, path string, r io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, f.contentURL(path), r)
	if err != nil {
		return err
	}
	f.client.authHeaders(req.Header)
	req.Header.Set("Content-Type", "application/octet-stream")
	hc := f.client.http
	if hc.Timeout > 0 {
		clone := *hc
		clone.Timeout = 0
		hc = &clone
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return parseAPIError(resp.StatusCode, data)
	}
	return nil
}

// Stat returns metadata for a path.
func (f *SandboxFS) Stat(ctx context.Context, path string) (*FsMetadata, error) {
	var raw map[string]any
	if err := f.client.doJSON(ctx, http.MethodGet, f.opURL("stat", path), nil, &raw); err != nil {
		return nil, err
	}
	return decodeFsMetadata(raw), nil
}

// List lists directory entries.
func (f *SandboxFS) List(ctx context.Context, path string) ([]FsEntry, error) {
	var out struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := f.client.doJSON(ctx, http.MethodGet, f.opURL("list", path), nil, &out); err != nil {
		return nil, err
	}
	entries := make([]FsEntry, 0, len(out.Entries))
	for _, e := range out.Entries {
		entries = append(entries, decodeFsEntry(e))
	}
	return entries, nil
}

// Exists reports whether a path exists.
func (f *SandboxFS) Exists(ctx context.Context, path string) (bool, error) {
	var out struct {
		Exists bool `json:"exists"`
	}
	if err := f.client.doJSON(ctx, http.MethodGet, f.opURL("exists", path), nil, &out); err != nil {
		return false, err
	}
	return out.Exists, nil
}

// Mkdir creates a directory. Parents must exist.
func (f *SandboxFS) Mkdir(ctx context.Context, path string) error {
	return f.client.doJSON(ctx, http.MethodPost,
		"/v1/sandboxes/"+url.PathEscape(f.sandboxID)+"/fs/mkdir",
		map[string]string{"path": path}, nil)
}

// Remove removes a file.
func (f *SandboxFS) Remove(ctx context.Context, path string) error {
	return f.client.doJSON(ctx, http.MethodPost,
		"/v1/sandboxes/"+url.PathEscape(f.sandboxID)+"/fs/remove",
		map[string]string{"path": path}, nil)
}

// RemoveDir removes an empty directory.
func (f *SandboxFS) RemoveDir(ctx context.Context, path string) error {
	return f.client.doJSON(ctx, http.MethodPost,
		"/v1/sandboxes/"+url.PathEscape(f.sandboxID)+"/fs/remove-dir",
		map[string]string{"path": path}, nil)
}

// Copy copies a file within the sandbox.
func (f *SandboxFS) Copy(ctx context.Context, from, to string) error {
	return f.client.doJSON(ctx, http.MethodPost,
		"/v1/sandboxes/"+url.PathEscape(f.sandboxID)+"/fs/copy",
		map[string]string{"from": from, "to": to}, nil)
}

// Rename renames or moves a path within the sandbox.
func (f *SandboxFS) Rename(ctx context.Context, from, to string) error {
	return f.client.doJSON(ctx, http.MethodPost,
		"/v1/sandboxes/"+url.PathEscape(f.sandboxID)+"/fs/rename",
		map[string]string{"from": from, "to": to}, nil)
}

func decodeFsTime(v any) *time.Time {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return nil
		}
	}
	u := t.UTC()
	return &u
}

func decodeFsMetadata(raw map[string]any) *FsMetadata {
	m := &FsMetadata{
		Kind:     FsEntryKind(fmt.Sprint(raw["kind"])),
		Readonly: raw["readonly"] == true,
		Modified: decodeFsTime(raw["modified"]),
		Created:  decodeFsTime(raw["created"]),
	}
	m.Size = jsonNumberInt64(raw["size"])
	m.Mode = uint32(jsonNumberInt64(raw["mode"]))
	return m
}

func decodeFsEntry(raw map[string]any) FsEntry {
	return FsEntry{
		Path:     fmt.Sprint(raw["path"]),
		Kind:     FsEntryKind(fmt.Sprint(raw["kind"])),
		Size:     jsonNumberInt64(raw["size"]),
		Mode:     uint32(jsonNumberInt64(raw["mode"])),
		Modified: decodeFsTime(raw["modified"]),
	}
}

func jsonNumberInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}
