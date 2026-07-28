package scheduler_test

import (
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/scheduler"
)

func TestSelectNodeSpread(t *testing.T) {
	now := time.Now()
	nodes := []*node.Node{
		{ID: "a", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
		{ID: "b", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	sbs := []*sandbox.Sandbox{
		{ID: "1", NodeID: "a", DesiredState: sandbox.DesiredRunning},
		{ID: "2", NodeID: "a", DesiredState: sandbox.DesiredRunning},
	}
	got := scheduler.SelectNode(nodes, sbs, now)
	if got != "b" {
		t.Fatalf("got %s want b", got)
	}
}

func TestSelectNodeFallbackWithoutHeartbeat(t *testing.T) {
	nodes := []*node.Node{
		{ID: "only", Membership: node.MembershipAccepted},
	}
	got := scheduler.SelectNode(nodes, nil, time.Now())
	if got != "only" {
		t.Fatalf("got %q", got)
	}
}

func TestSelectNodeSkipsPauseAndDrain(t *testing.T) {
	now := time.Now()
	nodes := []*node.Node{
		{ID: "paused", Membership: node.MembershipAccepted, Availability: node.AvailabilityPause, RuntimeHeartbeatAt: now},
		{ID: "drained", Membership: node.MembershipAccepted, Availability: node.AvailabilityDrain, RuntimeHeartbeatAt: now},
		{ID: "active", Membership: node.MembershipAccepted, Availability: node.AvailabilityActive, RuntimeHeartbeatAt: now},
	}
	got := scheduler.SelectNode(nodes, nil, now)
	if got != "active" {
		t.Fatalf("got %q want active", got)
	}
}
