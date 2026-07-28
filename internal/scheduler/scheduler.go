package scheduler

import (
	"time"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

// HeartbeatStaleAfter is how long after the last heartbeat a node is ineligible.
const HeartbeatStaleAfter = 30 * time.Second

// SelectNode picks a live runtime node using spread (fewest sandboxes).
// Prefer nodes with a recent heartbeat; fall back to any accepted node if none are live
// (bootstrap / first sandbox before the first heartbeat lands).
// Nodes with availability pause or drain are never selected.
func SelectNode(nodes []*node.Node, sandboxes []*sandbox.Sandbox, now time.Time) string {
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

// IsNodeLive reports whether the node's runtime heartbeat is fresh.
func IsNodeLive(n *node.Node, now time.Time) bool {
	if n == nil || n.RuntimeHeartbeatAt.IsZero() {
		return false
	}
	return now.Sub(n.RuntimeHeartbeatAt) <= HeartbeatStaleAfter
}
