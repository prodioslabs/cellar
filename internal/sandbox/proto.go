package sandbox

import (
	"encoding/json"
	"time"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

// SpecFromJSON unmarshals a cloud Spec from JSON.
func SpecFromJSON(b []byte) (Spec, error) {
	if len(b) == 0 {
		return Spec{}, nil
	}
	var spec Spec
	if err := json.Unmarshal(b, &spec); err != nil {
		return Spec{}, err
	}
	return NormalizeSpec(spec), nil
}

// SpecToJSON marshals a Spec to JSON.
func SpecToJSON(spec Spec) ([]byte, error) {
	return json.Marshal(spec)
}

// StatusFromProto converts proto status.
func StatusFromProto(p *cellarv1.SandboxStatus) Status {
	if p == nil {
		return Status{}
	}
	st := Status{
		Phase:     StatusPhase(p.Phase),
		LocalName: p.LocalName,
		Message:   p.Message,
	}
	if p.StartedAtUnixNano > 0 {
		st.StartedAt = time.Unix(0, p.StartedAtUnixNano).UTC()
	}
	if p.StoppedAtUnixNano > 0 {
		st.StoppedAt = time.Unix(0, p.StoppedAtUnixNano).UTC()
	}
	if p.UpdatedAtUnixNano > 0 {
		st.UpdatedAt = time.Unix(0, p.UpdatedAtUnixNano).UTC()
	}
	return st
}

// StatusToProto converts Go status.
func StatusToProto(st Status) *cellarv1.SandboxStatus {
	out := &cellarv1.SandboxStatus{
		Phase:     string(st.Phase),
		LocalName: st.LocalName,
		Message:   st.Message,
	}
	if !st.StartedAt.IsZero() {
		out.StartedAtUnixNano = st.StartedAt.UnixNano()
	}
	if !st.StoppedAt.IsZero() {
		out.StoppedAtUnixNano = st.StoppedAt.UnixNano()
	}
	if !st.UpdatedAt.IsZero() {
		out.UpdatedAtUnixNano = st.UpdatedAt.UnixNano()
	}
	return out
}

// ToProto converts a Sandbox.
func ToProto(s *Sandbox) *cellarv1.Sandbox {
	if s == nil {
		return nil
	}
	specJSON, _ := SpecToJSON(s.Spec)
	var labelsJSON []byte
	if len(s.Labels) > 0 {
		labelsJSON, _ = json.Marshal(s.Labels)
	}
	out := &cellarv1.Sandbox{
		Id:                   s.ID,
		Name:                 s.Name,
		Slug:                 s.Slug,
		SpecJson:             specJSON,
		NodeId:               s.NodeID,
		DesiredState:         string(s.DesiredState),
		Status:               StatusToProto(s.Status),
		Ephemeral:            s.Ephemeral,
		LabelsJson:           labelsJSON,
		AssignmentGeneration: s.AssignmentGeneration,
	}
	if !s.CreatedAt.IsZero() {
		out.CreatedAtUnixNano = s.CreatedAt.UnixNano()
	}
	if !s.UpdatedAt.IsZero() {
		out.UpdatedAtUnixNano = s.UpdatedAt.UnixNano()
	}
	return out
}

// FromProto converts a proto Sandbox.
func FromProto(p *cellarv1.Sandbox) *Sandbox {
	if p == nil {
		return nil
	}
	spec, _ := SpecFromJSON(p.SpecJson)
	s := &Sandbox{
		ID:                   p.Id,
		Name:                 p.Name,
		Slug:                 p.Slug,
		Spec:                 spec,
		NodeID:               p.NodeId,
		DesiredState:         DesiredState(p.DesiredState),
		Status:               StatusFromProto(p.Status),
		Ephemeral:            p.Ephemeral,
		AssignmentGeneration: p.AssignmentGeneration,
	}
	if len(p.LabelsJson) > 0 {
		_ = json.Unmarshal(p.LabelsJson, &s.Labels)
	}
	if p.CreatedAtUnixNano > 0 {
		s.CreatedAt = time.Unix(0, p.CreatedAtUnixNano).UTC()
	}
	if p.UpdatedAtUnixNano > 0 {
		s.UpdatedAt = time.Unix(0, p.UpdatedAtUnixNano).UTC()
	}
	return s
}

// VolumeToProto converts a Volume.
func VolumeToProto(v *Volume) *cellarv1.Volume {
	if v == nil {
		return nil
	}
	var labelsJSON []byte
	if len(v.Labels) > 0 {
		labelsJSON, _ = json.Marshal(v.Labels)
	}
	out := &cellarv1.Volume{
		Id:         v.ID,
		Name:       v.Name,
		Kind:       string(v.Kind),
		Status:     string(v.Status),
		NodeId:     v.NodeID,
		LabelsJson: labelsJSON,
	}
	if v.UsedBytes != nil {
		out.UsedBytes = v.UsedBytes
	}
	if v.CapacityBytes != nil {
		out.CapacityBytes = v.CapacityBytes
	}
	if v.CapacityGiB != nil {
		out.CapacityGib = v.CapacityGiB
	}
	if !v.CreatedAt.IsZero() {
		out.CreatedAtUnixNano = v.CreatedAt.UnixNano()
	}
	if !v.UpdatedAt.IsZero() {
		out.UpdatedAtUnixNano = v.UpdatedAt.UnixNano()
	}
	return out
}

// VolumeFromProto converts a proto Volume.
func VolumeFromProto(p *cellarv1.Volume) *Volume {
	if p == nil {
		return nil
	}
	v := &Volume{
		ID:     p.Id,
		Name:   p.Name,
		Kind:   VolumeKind(p.Kind),
		Status: VolumeStatus(p.Status),
		NodeID: p.NodeId,
	}
	if p.UsedBytes != nil {
		u := p.GetUsedBytes()
		v.UsedBytes = &u
	}
	if p.CapacityBytes != nil {
		u := p.GetCapacityBytes()
		v.CapacityBytes = &u
	}
	if p.CapacityGib != nil {
		u := p.GetCapacityGib()
		v.CapacityGiB = &u
	}
	if len(p.LabelsJson) > 0 {
		_ = json.Unmarshal(p.LabelsJson, &v.Labels)
	}
	if p.CreatedAtUnixNano > 0 {
		v.CreatedAt = time.Unix(0, p.CreatedAtUnixNano).UTC()
	}
	if p.UpdatedAtUnixNano > 0 {
		v.UpdatedAt = time.Unix(0, p.UpdatedAtUnixNano).UTC()
	}
	return v
}
