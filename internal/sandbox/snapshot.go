package sandbox

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

// FSSnapshotKind is how a filesystem snapshot was created.
type FSSnapshotKind string

const (
	FSSnapshotKindAutomatic FSSnapshotKind = "automatic"
	FSSnapshotKindManual    FSSnapshotKind = "manual"
)

// FSSnapshotStatus is the control-plane lifecycle of a filesystem snapshot.
type FSSnapshotStatus string

const (
	FSSnapshotStatusCreating FSSnapshotStatus = "creating"
	FSSnapshotStatusCreated  FSSnapshotStatus = "created"
	FSSnapshotStatusFailed   FSSnapshotStatus = "failed"
	FSSnapshotStatusDeleted  FSSnapshotStatus = "deleted"
)

// FSSnapshotBackend is where the tar object lives.
type FSSnapshotBackend string

const (
	FSSnapshotBackendLocal FSSnapshotBackend = "local"
	FSSnapshotBackendS3    FSSnapshotBackend = "s3"
)

// DefaultSnapshotExpiration is used when snapshot_expiration_ms is nil.
const DefaultSnapshotExpiration = 30 * 24 * time.Hour

// KeepLastSnapshots bounds how many snapshots are retained per sandbox.
type KeepLastSnapshots struct {
	Count         uint32 `json:"count"`
	ExpirationMs  *int64 `json:"expiration_ms,omitempty"`
	DeleteEvicted *bool  `json:"delete_evicted,omitempty"`
}

// CloneKeepLastSnapshots returns a deep copy.
func CloneKeepLastSnapshots(k *KeepLastSnapshots) *KeepLastSnapshots {
	if k == nil {
		return nil
	}
	cp := *k
	if k.ExpirationMs != nil {
		v := *k.ExpirationMs
		cp.ExpirationMs = &v
	}
	if k.DeleteEvicted != nil {
		v := *k.DeleteEvicted
		cp.DeleteEvicted = &v
	}
	return &cp
}

// ShouldDeleteEvicted reports whether evicted snapshots should be removed.
func (k *KeepLastSnapshots) ShouldDeleteEvicted() bool {
	if k == nil || k.DeleteEvicted == nil {
		return true
	}
	return *k.DeleteEvicted
}

// FSSnapshot is a Raft-replicated filesystem snapshot record.
// The tar object lives in BlobStore at Location; this type is metadata only.
type FSSnapshot struct {
	ID           string            `json:"id"`
	SandboxID    string            `json:"sandbox_id"`
	SandboxName  string            `json:"sandbox_name"`
	NodeID       string            `json:"node_id,omitempty"`
	Kind         FSSnapshotKind    `json:"kind"`
	Status       FSSnapshotStatus  `json:"status"`
	Backend      FSSnapshotBackend `json:"backend"`
	Location     string            `json:"location"`
	SizeBytes    int64             `json:"size_bytes,omitempty"`
	Digest       string            `json:"digest,omitempty"`
	SpecJSON     []byte            `json:"spec_json,omitempty"`
	ExpirationMs int64             `json:"expiration_ms"` // 0 = never expire
	LastUsedAt   time.Time         `json:"last_used_at"`
	ExpiresAt    time.Time         `json:"expires_at,omitempty"`
	Message      string            `json:"message,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// CloneFSSnapshot returns a deep copy.
func CloneFSSnapshot(s *FSSnapshot) *FSSnapshot {
	if s == nil {
		return nil
	}
	cp := *s
	if s.SpecJSON != nil {
		cp.SpecJSON = append([]byte(nil), s.SpecJSON...)
	}
	return &cp
}

// SnapshotObjectKey returns snapshots/{sandbox_name}/snapshot_{snapshot_id}.tar.
func SnapshotObjectKey(sandboxName, snapshotID string) string {
	return path.Join("snapshots", SanitizeSnapshotName(sandboxName), "snapshot_"+snapshotID+".tar")
}

// SanitizeSnapshotName strips path separators from a sandbox name used as an object key segment.
func SanitizeSnapshotName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unnamed"
	}
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "..", "_")
	return name
}

// HasSessionTimers reports whether the lifecycle policy will stop the VM in microsandbox.
func (l LifecyclePolicy) HasSessionTimers() bool {
	return (l.MaxDurationSecs != nil && *l.MaxDurationSecs > 0) ||
		(l.IdleTimeoutSecs != nil && *l.IdleTimeoutSecs > 0)
}

// EffectiveSnapshotExpirationMs returns the TTL in milliseconds.
// nil means the 30-day default; 0 means never expire.
func (l LifecyclePolicy) EffectiveSnapshotExpirationMs() int64 {
	if l.SnapshotExpirationMs == nil {
		return DefaultSnapshotExpiration.Milliseconds()
	}
	return *l.SnapshotExpirationMs
}

// ComputeExpiresAt returns the expiration timestamp from last use.
// expirationMs 0 means never expire (zero time).
func ComputeExpiresAt(lastUsed time.Time, expirationMs int64) time.Time {
	if expirationMs <= 0 {
		return time.Time{}
	}
	if lastUsed.IsZero() {
		lastUsed = time.Now().UTC()
	}
	return lastUsed.Add(time.Duration(expirationMs) * time.Millisecond)
}

// TouchUsage resets last-used and recomputes expires_at.
func (s *FSSnapshot) TouchUsage(now time.Time) {
	if s == nil {
		return
	}
	s.LastUsedAt = now
	s.ExpiresAt = ComputeExpiresAt(now, s.ExpirationMs)
	s.UpdatedAt = now
}

// IsExpired reports whether the snapshot has passed ExpiresAt.
func (s *FSSnapshot) IsExpired(now time.Time) bool {
	if s == nil || s.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(s.ExpiresAt)
}

// SessionTimeoutMessage infers idle vs max-duration when the VM is already down.
func SessionTimeoutMessage(spec Spec, startedAt time.Time, now time.Time) string {
	if spec.Lifecycle.MaxDurationSecs != nil && *spec.Lifecycle.MaxDurationSecs > 0 && !startedAt.IsZero() {
		limit := startedAt.Add(time.Duration(*spec.Lifecycle.MaxDurationSecs) * time.Second)
		if !now.Before(limit) {
			return "max_duration"
		}
	}
	if spec.Lifecycle.IdleTimeoutSecs != nil && *spec.Lifecycle.IdleTimeoutSecs > 0 {
		return "idle_timeout"
	}
	if spec.Lifecycle.HasSessionTimers() {
		return "session_timeout"
	}
	return ""
}

// ParseExpirationMs accepts 0/none, a millisecond integer, or a duration like 14d, 7h, 30m.
func ParseExpirationMs(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return DefaultSnapshotExpiration.Milliseconds(), nil
	}
	if s == "0" || s == "none" || s == "never" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("expiration must be >= 0")
		}
		return n, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		// 14d is not a time.ParseDuration unit
		if strings.HasSuffix(s, "d") {
			days, perr := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
			if perr != nil {
				return 0, fmt.Errorf("invalid expiration %q", s)
			}
			return int64(days * 24 * float64(time.Hour) / float64(time.Millisecond)), nil
		}
		return 0, fmt.Errorf("invalid expiration %q", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("expiration must be >= 0")
	}
	return d.Milliseconds(), nil
}

// ValidateKeepLastSnapshots checks the retention cap.
func ValidateKeepLastSnapshots(k *KeepLastSnapshots) error {
	if k == nil {
		return nil
	}
	if k.Count < 1 || k.Count > 10 {
		return fmt.Errorf("keep_last_snapshots.count must be between 1 and 10")
	}
	if k.ExpirationMs != nil && *k.ExpirationMs < 0 {
		return fmt.Errorf("keep_last_snapshots.expiration_ms must be >= 0")
	}
	return nil
}
