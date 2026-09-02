package scheduler_test

import (
	"strings"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/scheduler"
)

func TestPlanEvictionReassignsRunning(t *testing.T) {
	now := time.Now().UTC()
	deadHB := now.Add(-(scheduler.HeartbeatEvictAfter + time.Second))
	nodes := []*node.Node{
		{ID: "dead", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: deadHB},
		{ID: "live", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{
			ID: "sb1", NodeID: "dead", DesiredState: sandbox.DesiredRunning,
			AssignmentGeneration: 1,
			Spec:                 sandbox.Spec{Image: "alpine"},
			Status:               sandbox.Status{Phase: sandbox.PhaseRunning, ContainerID: "c1"},
		},
		{
			ID: "sb-stopped", NodeID: "dead", DesiredState: sandbox.DesiredStopped,
			AssignmentGeneration: 1,
			Spec:                 sandbox.Spec{Image: "alpine"},
		},
	}
	decisions := scheduler.PlanEviction(nodes, sbs, now, nil)
	if len(decisions) != 1 {
		t.Fatalf("got %d decisions want 1", len(decisions))
	}
	d := decisions[0]
	if d.Sandbox.ID != "sb1" || d.Sandbox.NodeID != "live" {
		t.Fatalf("got %+v", d.Sandbox)
	}
	if d.Sandbox.AssignmentGeneration != 2 {
		t.Fatalf("generation=%d want 2", d.Sandbox.AssignmentGeneration)
	}
	if d.Sandbox.Status.Phase != sandbox.PhasePending {
		t.Fatalf("phase=%s", d.Sandbox.Status.Phase)
	}
	if d.Sandbox.Status.ContainerID != "" {
		t.Fatal("container id should be cleared")
	}
	if !strings.Contains(d.Sandbox.Status.Message, "rescheduled") {
		t.Fatalf("message=%q", d.Sandbox.Status.Message)
	}
	if d.SourceNodeID != "dead" || d.FailedMount {
		t.Fatalf("meta: %+v", d)
	}
}

func TestPlanEvictionNoTargetIsNoop(t *testing.T) {
	now := time.Now().UTC()
	deadHB := now.Add(-(scheduler.HeartbeatEvictAfter + time.Second))
	nodes := []*node.Node{
		{ID: "dead", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: deadHB},
	}
	sbs := []*sandbox.Sandbox{
		{ID: "sb1", NodeID: "dead", DesiredState: sandbox.DesiredRunning, Spec: sandbox.Spec{Image: "alpine"}},
	}
	if got := scheduler.PlanEviction(nodes, sbs, now, nil); len(got) != 0 {
		t.Fatalf("expected no decisions, got %d", len(got))
	}
}

func TestPlanEvictionHostMountsFail(t *testing.T) {
	now := time.Now().UTC()
	deadHB := now.Add(-(scheduler.HeartbeatEvictAfter + time.Second))
	nodes := []*node.Node{
		{ID: "dead", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: deadHB},
		{ID: "live", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{
			ID: "sb1", NodeID: "dead", DesiredState: sandbox.DesiredRunning,
			AssignmentGeneration: 3,
			Spec: sandbox.Spec{
				Image:  "alpine",
				Mounts: []sandbox.Mount{{Source: "/host", Target: "/mnt"}},
			},
		},
	}
	decisions := scheduler.PlanEviction(nodes, sbs, now, nil)
	if len(decisions) != 1 {
		t.Fatalf("got %d", len(decisions))
	}
	d := decisions[0]
	if !d.FailedMount {
		t.Fatal("expected FailedMount")
	}
	if d.Sandbox.NodeID != "dead" {
		t.Fatalf("node_id should stay on dead node, got %s", d.Sandbox.NodeID)
	}
	if d.Sandbox.AssignmentGeneration != 3 {
		t.Fatalf("generation should be unchanged, got %d", d.Sandbox.AssignmentGeneration)
	}
	if d.Sandbox.Status.Phase != sandbox.PhaseFailed {
		t.Fatalf("phase=%s", d.Sandbox.Status.Phase)
	}
	if !strings.Contains(d.Sandbox.Status.Message, "host mounts") {
		t.Fatalf("message=%q", d.Sandbox.Status.Message)
	}
}

func TestPlanEvictionRespectsQuarantineExclude(t *testing.T) {
	now := time.Now().UTC()
	deadHB := now.Add(-(scheduler.HeartbeatEvictAfter + time.Second))
	nodes := []*node.Node{
		{ID: "dead", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: deadHB},
		{ID: "quarantined", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
		{ID: "live", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{ID: "sb1", NodeID: "dead", DesiredState: sandbox.DesiredRunning, AssignmentGeneration: 1, Spec: sandbox.Spec{Image: "alpine"}},
	}
	decisions := scheduler.PlanEviction(nodes, sbs, now, map[string]struct{}{"quarantined": {}})
	if len(decisions) != 1 || decisions[0].Sandbox.NodeID != "live" {
		t.Fatalf("got %+v", decisions)
	}
}

func TestPlanEvictionSpreadsAcrossTargets(t *testing.T) {
	now := time.Now().UTC()
	deadHB := now.Add(-(scheduler.HeartbeatEvictAfter + time.Second))
	nodes := []*node.Node{
		{ID: "dead", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: deadHB},
		{ID: "a", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
		{ID: "b", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{ID: "1", NodeID: "dead", DesiredState: sandbox.DesiredRunning, AssignmentGeneration: 1, Spec: sandbox.Spec{Image: "alpine"}},
		{ID: "2", NodeID: "dead", DesiredState: sandbox.DesiredRunning, AssignmentGeneration: 1, Spec: sandbox.Spec{Image: "alpine"}},
	}
	decisions := scheduler.PlanEviction(nodes, sbs, now, nil)
	if len(decisions) != 2 {
		t.Fatalf("got %d", len(decisions))
	}
	seen := map[string]struct{}{}
	for _, d := range decisions {
		seen[d.Sandbox.NodeID] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("expected spread across targets, got %v", seen)
	}
}
