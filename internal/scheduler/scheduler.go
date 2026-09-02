package scheduler

import (
	"time"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

// HeartbeatStaleAfter is how long after the last heartbeat a node is ineligible
// for new placement (T_ready). Derived status is ready|down from this window.
const HeartbeatStaleAfter = 30 * time.Second

// HeartbeatEvictAfter is how long after the last heartbeat a node's running
// sandboxes may be recreated elsewhere (T_evict). Must exceed HeartbeatStaleAfter
// so a short blip does not destroy in-container state.
const HeartbeatEvictAfter = 60 * time.Second

// QuarantineHeartbeats is how many consecutive successful heartbeats a node
// must send after eviction before it is eligible for new placement again.
const QuarantineHeartbeats = 3

// SelectOpts controls SelectNode eligibility beyond membership and availability.
type SelectOpts struct {
	// ExcludeNodeIDs are never chosen (e.g. the node being evacuated, quarantined nodes).
	ExcludeNodeIDs map[string]struct{}
}

// SelectNode picks a live runtime node using spread (fewest sandboxes).
// Prefer nodes with a recent heartbeat; fall back to any accepted node if none are live
// (bootstrap / first sandbox before the first heartbeat lands).
// Nodes with availability pause or drain are never selected.
func SelectNode(nodes []*node.Node, sandboxes []*sandbox.Sandbox, now time.Time) string {
	return SelectNodeOpts(nodes, sandboxes, now, SelectOpts{})
}

// SelectNodeOpts is SelectNode with optional exclusions (quarantine / evacuation source).
func SelectNodeOpts(nodes []*node.Node, sandboxes []*sandbox.Sandbox, now time.Time, opts SelectOpts) string {
	counts := map[string]int{}
	for _, sb := range sandboxes {
		if sb == nil || sb.NodeID == "" {
			continue
		}
		if sb.DesiredState == sandbox.DesiredRemoved {
			continue
		}
		counts[sb.NodeID]++
	}

	var bestLive string
	bestLiveCount := int(^uint(0) >> 1)
	var bestAny string
	bestAnyCount := int(^uint(0) >> 1)

	for _, n := range nodes {
		if n == nil || n.ID == "" || n.Membership != node.MembershipAccepted {
			continue
		}
		if !n.Availability.Schedulable() {
			continue
		}
		if opts.ExcludeNodeIDs != nil {
			if _, skip := opts.ExcludeNodeIDs[n.ID]; skip {
				continue
			}
		}
		c := counts[n.ID]
		if c < bestAnyCount {
			bestAnyCount = c
			bestAny = n.ID
		}
		if n.RuntimeHeartbeatAt.IsZero() {
			continue
		}
		if now.Sub(n.RuntimeHeartbeatAt) > HeartbeatStaleAfter {
			continue
		}
		if c < bestLiveCount {
			bestLiveCount = c
			bestLive = n.ID
		}
	}
	if bestLive != "" {
		return bestLive
	}
	return bestAny
}

// IsNodeLive reports whether the node's runtime heartbeat is fresh (T_ready).
func IsNodeLive(n *node.Node, now time.Time) bool {
	if n == nil || n.RuntimeHeartbeatAt.IsZero() {
		return false
	}
	return now.Sub(n.RuntimeHeartbeatAt) <= HeartbeatStaleAfter
}

// IsNodeEvictable reports whether the node's heartbeat is old enough that its
// running sandboxes may be reassigned (T_evict). Nodes that have never
// heartbeated are not evictable (bootstrap).
func IsNodeEvictable(n *node.Node, now time.Time) bool {
	if n == nil || n.Membership != node.MembershipAccepted {
		return false
	}
	if n.RuntimeHeartbeatAt.IsZero() {
		return false
	}
	return now.Sub(n.RuntimeHeartbeatAt) > HeartbeatEvictAfter
}

// HasLiveSchedulableNode reports whether any accepted, schedulable node has a
// fresh heartbeat (excluding excludeIDs). Used before eviction so sandboxes
// are not left unassigned when no target exists.
func HasLiveSchedulableNode(nodes []*node.Node, now time.Time, excludeIDs map[string]struct{}) bool {
	for _, n := range nodes {
		if n == nil || n.ID == "" || n.Membership != node.MembershipAccepted {
			continue
		}
		if !n.Availability.Schedulable() {
			continue
		}
		if excludeIDs != nil {
			if _, skip := excludeIDs[n.ID]; skip {
				continue
			}
		}
		if IsNodeLive(n, now) {
			return true
		}
	}
	return false
}
