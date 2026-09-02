package sandbox_test

import (
	"errors"
	"testing"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestCheckAssignmentGeneration(t *testing.T) {
	if err := sandbox.CheckAssignmentGeneration(0, 0); err != nil {
		t.Fatalf("legacy zero: %v", err)
	}
	if err := sandbox.CheckAssignmentGeneration(0, 5); err != nil {
		t.Fatalf("legacy store accepts any: %v", err)
	}
	if err := sandbox.CheckAssignmentGeneration(2, 2); err != nil {
		t.Fatalf("matching: %v", err)
	}
	err := sandbox.CheckAssignmentGeneration(2, 1)
	if !errors.Is(err, sandbox.ErrStaleAssignment) {
		t.Fatalf("want ErrStaleAssignment, got %v", err)
	}
	err = sandbox.CheckAssignmentGeneration(2, 0)
	if !errors.Is(err, sandbox.ErrStaleAssignment) {
		t.Fatalf("want ErrStaleAssignment for zero report, got %v", err)
	}
}

func TestToFromProtoAssignmentGeneration(t *testing.T) {
	sb := &sandbox.Sandbox{
		ID:                   "x",
		DesiredState:         sandbox.DesiredRunning,
		AssignmentGeneration: 7,
		Spec:                 sandbox.Spec{Image: "alpine"},
	}
	p := sandbox.ToProto(sb)
	if p.GetAssignmentGeneration() != 7 {
		t.Fatalf("proto gen=%d", p.GetAssignmentGeneration())
	}
	back := sandbox.FromProto(p)
	if back.AssignmentGeneration != 7 {
		t.Fatalf("roundtrip gen=%d", back.AssignmentGeneration)
	}
}
