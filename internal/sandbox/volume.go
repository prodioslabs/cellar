package sandbox

import (
	"fmt"
	"time"
)

// VolumeKind is the cloud volume kind.
type VolumeKind string

const (
	VolumeKindDefault VolumeKind = "default"
	VolumeKindNamed   VolumeKind = "named"
)

// VolumeStatus is the cloud volume lifecycle status.
type VolumeStatus string

const (
	VolumeStatusReady    VolumeStatus = "ready"
	VolumeStatusDeleting VolumeStatus = "deleting"
)

// Volume is a Raft-replicated named/default volume.
type Volume struct {
	ID            string            `json:"id"`
	Name          string            `json:"name,omitempty"` // empty for default
	Kind          VolumeKind        `json:"kind"`
	Status        VolumeStatus      `json:"status"`
	NodeID        string            `json:"node_id,omitempty"`
	UsedBytes     *uint64           `json:"used_bytes,omitempty"`
	CapacityBytes *uint64           `json:"capacity_bytes,omitempty"`
	CapacityGiB   *uint32           `json:"capacity_gib,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// CloneVolume returns a deep copy.
func CloneVolume(v *Volume) *Volume {
	if v == nil {
		return nil
	}
	cp := *v
	if v.Labels != nil {
		cp.Labels = make(map[string]string, len(v.Labels))
		for k, val := range v.Labels {
			cp.Labels[k] = val
		}
	}
	if v.UsedBytes != nil {
		u := *v.UsedBytes
		cp.UsedBytes = &u
	}
	if v.CapacityBytes != nil {
		u := *v.CapacityBytes
		cp.CapacityBytes = &u
	}
	if v.CapacityGiB != nil {
		u := *v.CapacityGiB
		cp.CapacityGiB = &u
	}
	return &cp
}

// ValidateVolumeCreate checks create inputs.
func ValidateVolumeCreate(name string, capacityGiB *uint32) error {
	if stringsTrim(name) == "" {
		return fmt.Errorf("volume name is required")
	}
	if capacityGiB != nil && *capacityGiB == 0 {
		return fmt.Errorf("capacity_gib must be >= 1 when set")
	}
	return nil
}

func stringsTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
