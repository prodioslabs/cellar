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

func TestIsNodeEvictable(t *testing.T) {
	now := time.Now()
	live := &node.Node{ID: "a", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now}
	if scheduler.IsNodeEvictable(live, now) {
		t.Fatal("fresh heartbeat must not be evictable")
	}
	staleReady := &node.Node{
		ID: "b", Membership: node.MembershipAccepted,
		RuntimeHeartbeatAt: now.Add(-(scheduler.HeartbeatStaleAfter + time.Second)),
	}
	if scheduler.IsNodeEvictable(staleReady, now) {
		t.Fatal("T_ready alone must not make a node evictable")
	}
	dead := &node.Node{
		ID: "c", Membership: node.MembershipAccepted,
		RuntimeHeartbeatAt: now.Add(-(scheduler.HeartbeatEvictAfter + time.Second)),
	}
	if !scheduler.IsNodeEvictable(dead, now) {
		t.Fatal("heartbeat older than T_evict must be evictable")
	}
	never := &node.Node{ID: "d", Membership: node.MembershipAccepted}
	if scheduler.IsNodeEvictable(never, now) {
		t.Fatal("never-heartbeated nodes must not be evictable")
	}
}

func TestSelectNodeOptsExclude(t *testing.T) {
	now := time.Now()
	nodes := []*node.Node{
		{ID: "a", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
		{ID: "b", Membership: node.MembershipAccepted, RuntimeHeartbeatAt: now},
	}
	got := scheduler.SelectNodeOpts(nodes, nil, now, scheduler.SelectOpts{
		ExcludeNodeIDs: map[string]struct{}{"a": {}},
	})
	if got != "b" {
		t.Fatalf("got %q want b", got)
	}
}

func TestQuarantineClearsAfterHeartbeats(t *testing.T) {
	q := scheduler.NewQuarantine()
	q.MarkEvicted("n1")
	if !q.Contains("n1") {
		t.Fatal("expected quarantined")
	}
	for i := 0; i < scheduler.QuarantineHeartbeats-1; i++ {
		q.NoteHeartbeat("n1")
		if !q.Contains("n1") {
			t.Fatalf("cleared too early at heartbeat %d", i+1)
		}
	}
	q.NoteHeartbeat("n1")
	if q.Contains("n1") {
		t.Fatal("expected quarantine cleared")
	}
}

