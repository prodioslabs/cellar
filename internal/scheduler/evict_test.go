package scheduler_test

import (
	"strings"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/scheduler"
)

func TestPlanVacateHeartbeatReassignsRunning(t *testing.T) {
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
	decisions := scheduler.PlanVacate(nodes, sbs, now, nil)
	if len(decisions) != 1 {
		t.Fatalf("got %d decisions want 1", len(decisions))
	}
	d := decisions[0]
	if d.Reason != scheduler.ReasonHeartbeat {
		t.Fatalf("reason=%q want heartbeat", d.Reason)
	}
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

func TestPlanVacateHeartbeatNoTargetIsNoop(t *testing.T) {
	now := time.Now().UTC()
	deadHB := now.Add(-(scheduler.HeartbeatEvictAfter + time.Second))
	nodes := []*node.Node{
		{ID: "dead", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: deadHB},
	}
	sbs := []*sandbox.Sandbox{
		{ID: "sb1", NodeID: "dead", DesiredState: sandbox.DesiredRunning, Spec: sandbox.Spec{Image: "alpine"}},
	}
	if got := scheduler.PlanVacate(nodes, sbs, now, nil); len(got) != 0 {
		t.Fatalf("expected no decisions, got %d", len(got))
	}
}

func TestPlanVacateHeartbeatHostMountsFail(t *testing.T) {
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
	decisions := scheduler.PlanVacate(nodes, sbs, now, nil)
	if len(decisions) != 1 {
		t.Fatalf("got %d", len(decisions))
	}
	d := decisions[0]
	if d.Reason != scheduler.ReasonHeartbeat {
		t.Fatalf("reason=%q want heartbeat", d.Reason)
	}
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

func TestPlanVacateRespectsQuarantineExclude(t *testing.T) {
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
	decisions := scheduler.PlanVacate(nodes, sbs, now, map[string]struct{}{"quarantined": {}})
	if len(decisions) != 1 || decisions[0].Sandbox.NodeID != "live" {
		t.Fatalf("got %+v", decisions)
	}
}

func TestPlanVacateSpreadsAcrossTargets(t *testing.T) {
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
	decisions := scheduler.PlanVacate(nodes, sbs, now, nil)
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

func TestPlanVacateDrainReassigns(t *testing.T) {
	now := time.Now().UTC()
	nodes := []*node.Node{
		{ID: "draining", Membership: node.MembershipAccepted, Availability: node.AvailabilityDrain, RuntimeHeartbeatAt: now},
		{ID: "live", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{
			ID: "sb1", NodeID: "draining", DesiredState: sandbox.DesiredRunning,
			AssignmentGeneration: 1,
			Spec:                 sandbox.Spec{Image: "alpine"},
			Status:               sandbox.Status{Phase: sandbox.PhaseRunning, ContainerID: "c1"},
		},
	}
	decisions := scheduler.PlanVacate(nodes, sbs, now, nil)
	if len(decisions) != 1 {
		t.Fatalf("got %d decisions want 1", len(decisions))
	}
	d := decisions[0]
	if d.Reason != scheduler.ReasonDrain {
		t.Fatalf("reason=%q want drain", d.Reason)
	}
	if d.Sandbox.NodeID != "live" || d.Sandbox.AssignmentGeneration != 2 {
		t.Fatalf("got node=%s gen=%d", d.Sandbox.NodeID, d.Sandbox.AssignmentGeneration)
	}
	if d.Sandbox.Status.Phase != sandbox.PhasePending || d.Sandbox.Status.ContainerID != "" {
		t.Fatalf("status=%+v", d.Sandbox.Status)
	}
	if !strings.Contains(d.Sandbox.Status.Message, "draining") {
		t.Fatalf("message=%q", d.Sandbox.Status.Message)
	}
}

func TestPlanVacateDrainLeavesHostMounts(t *testing.T) {
	now := time.Now().UTC()
	nodes := []*node.Node{
		{ID: "draining", Membership: node.MembershipAccepted, Availability: node.AvailabilityDrain, RuntimeHeartbeatAt: now},
		{ID: "live", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{
			ID: "sb1", NodeID: "draining", DesiredState: sandbox.DesiredRunning,
			AssignmentGeneration: 2,
			Spec: sandbox.Spec{
				Image:  "alpine",
				Mounts: []sandbox.Mount{{Source: "/host", Target: "/mnt"}},
			},
			Status: sandbox.Status{Phase: sandbox.PhaseRunning},
		},
	}
	if got := scheduler.PlanVacate(nodes, sbs, now, nil); len(got) != 0 {
		t.Fatalf("expected no decisions for drain+mounts, got %+v", got)
	}
}

func TestPlanVacatePauseDoesNotVacate(t *testing.T) {
	now := time.Now().UTC()
	nodes := []*node.Node{
		{ID: "paused", Membership: node.MembershipAccepted, Availability: node.AvailabilityPause, RuntimeHeartbeatAt: now},
		{ID: "live", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{ID: "sb1", NodeID: "paused", DesiredState: sandbox.DesiredRunning, AssignmentGeneration: 1, Spec: sandbox.Spec{Image: "alpine"}},
	}
	if got := scheduler.PlanVacate(nodes, sbs, now, nil); len(got) != 0 {
		t.Fatalf("pause must not vacate, got %+v", got)
	}
}

func TestPlanVacateDeadDrainIsHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	deadHB := now.Add(-(scheduler.HeartbeatEvictAfter + time.Second))
	nodes := []*node.Node{
		{ID: "dead", Membership: node.MembershipAccepted, Availability: node.AvailabilityDrain, RuntimeHeartbeatAt: deadHB},
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
	decisions := scheduler.PlanVacate(nodes, sbs, now, nil)
	if len(decisions) != 1 {
		t.Fatalf("got %d", len(decisions))
	}
	d := decisions[0]
	if d.Reason != scheduler.ReasonHeartbeat {
		t.Fatalf("reason=%q want heartbeat", d.Reason)
	}
	if !d.FailedMount || d.Sandbox.Status.Phase != sandbox.PhaseFailed {
		t.Fatalf("expected FailedMount, got %+v", d)
	}
	if d.Sandbox.NodeID != "dead" {
		t.Fatalf("node_id should stay, got %s", d.Sandbox.NodeID)
	}
}

func TestPlanVacateGoneReassigns(t *testing.T) {
	now := time.Now().UTC()
	nodes := []*node.Node{
		{ID: "live", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{
			ID: "sb1", NodeID: "deleted", DesiredState: sandbox.DesiredRunning,
			AssignmentGeneration: 1,
			Spec:                 sandbox.Spec{Image: "alpine"},
			Status:               sandbox.Status{Phase: sandbox.PhaseRunning, ContainerID: "c1"},
		},
	}
	decisions := scheduler.PlanVacate(nodes, sbs, now, nil)
	if len(decisions) != 1 {
		t.Fatalf("got %d want 1", len(decisions))
	}
	d := decisions[0]
	if d.Reason != scheduler.ReasonGone {
		t.Fatalf("reason=%q want gone", d.Reason)
	}
	if d.Sandbox.NodeID != "live" || d.SourceNodeID != "deleted" {
		t.Fatalf("got %+v", d)
	}
	if d.Sandbox.AssignmentGeneration != 2 {
		t.Fatalf("gen=%d", d.Sandbox.AssignmentGeneration)
	}
	if !strings.Contains(d.Sandbox.Status.Message, "left cluster") {
		t.Fatalf("message=%q", d.Sandbox.Status.Message)
	}
}

func TestPlanVacateGoneHostMountsFail(t *testing.T) {
	now := time.Now().UTC()
	nodes := []*node.Node{
		{ID: "live", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{
			ID: "sb1", NodeID: "deleted", DesiredState: sandbox.DesiredRunning,
			AssignmentGeneration: 4,
			Spec: sandbox.Spec{
				Image:  "alpine",
				Mounts: []sandbox.Mount{{Source: "/host", Target: "/mnt"}},
			},
		},
	}
	decisions := scheduler.PlanVacate(nodes, sbs, now, nil)
	if len(decisions) != 1 || !decisions[0].FailedMount {
		t.Fatalf("got %+v", decisions)
	}
	if decisions[0].Reason != scheduler.ReasonGone {
		t.Fatalf("reason=%q", decisions[0].Reason)
	}
	if decisions[0].Sandbox.NodeID != "deleted" {
		t.Fatalf("should stay on gone node, got %s", decisions[0].Sandbox.NodeID)
	}
}

func TestPlanVacateDrainNoTargetIsNoop(t *testing.T) {
	now := time.Now().UTC()
	nodes := []*node.Node{
		{ID: "draining", Membership: node.MembershipAccepted, Availability: node.AvailabilityDrain, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{ID: "sb1", NodeID: "draining", DesiredState: sandbox.DesiredRunning, Spec: sandbox.Spec{Image: "alpine"}},
	}
	if got := scheduler.PlanVacate(nodes, sbs, now, nil); len(got) != 0 {
		t.Fatalf("expected no decisions, got %d", len(got))
	}
}

func TestPlanVacateDrainDoesNotSelectDrainedPeer(t *testing.T) {
	now := time.Now().UTC()
	nodes := []*node.Node{
		{ID: "draining", Membership: node.MembershipAccepted, Availability: node.AvailabilityDrain, RuntimeHeartbeatAt: now},
		{ID: "also-drain", Membership: node.MembershipAccepted, Availability: node.AvailabilityDrain, RuntimeHeartbeatAt: now},
		{ID: "live", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{ID: "1", NodeID: "draining", DesiredState: sandbox.DesiredRunning, AssignmentGeneration: 1, Spec: sandbox.Spec{Image: "alpine"}},
		{ID: "2", NodeID: "draining", DesiredState: sandbox.DesiredRunning, AssignmentGeneration: 1, Spec: sandbox.Spec{Image: "alpine"}},
	}
	decisions := scheduler.PlanVacate(nodes, sbs, now, nil)
	if len(decisions) != 2 {
		t.Fatalf("got %d", len(decisions))
	}
	for _, d := range decisions {
		if d.Sandbox.NodeID != "live" {
			t.Fatalf("expected live target, got %s", d.Sandbox.NodeID)
		}
		if d.Reason != scheduler.ReasonDrain {
			t.Fatalf("reason=%q", d.Reason)
		}
	}
}
