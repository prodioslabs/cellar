package scheduler

import (
	"fmt"
	"time"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

// EvictDecision is a sandbox mutation produced by PlanEviction.
type EvictDecision struct {
	Sandbox *sandbox.Sandbox
	// SourceNodeID is the dead node the sandbox was taken from.
	SourceNodeID string
	// FailedMount is true when the sandbox was marked failed due to host mounts
	// (not reassigned).
	FailedMount bool
}

// PlanEviction returns sandbox updates for nodes whose heartbeats exceed
// HeartbeatEvictAfter. Running sandboxes without host mounts are reassigned
// onto live schedulable nodes (spread). Sandboxes with host mounts are marked
// failed and left on the dead node. Stopped/removed sandboxes are ignored.
//
// exclude lists node IDs that must not receive placements (quarantine).
// Callers should MarkEvicted for each distinct SourceNodeID that appears in
// the result when at least one sandbox was acted on.
func PlanEviction(nodes []*node.Node, sandboxes []*sandbox.Sandbox, now time.Time, exclude map[string]struct{}) []EvictDecision {
	if len(nodes) == 0 || len(sandboxes) == 0 {
		return nil
	}

	evictable := map[string]struct{}{}
	for _, n := range nodes {
		if IsNodeEvictable(n, now) {
			evictable[n.ID] = struct{}{}
		}
	}
	if len(evictable) == 0 {
		return nil
	}

	// Working copy of assignments so successive decisions see updated counts/node_ids.
	working := make([]*sandbox.Sandbox, 0, len(sandboxes))
	byID := make(map[string]*sandbox.Sandbox, len(sandboxes))
	for _, sb := range sandboxes {
		if sb == nil {
			continue
		}
		cp := sandbox.Clone(sb)
		working = append(working, cp)
		byID[cp.ID] = cp
	}

	var out []EvictDecision
	for _, sb := range sandboxes {
		if sb == nil || sb.DesiredState != sandbox.DesiredRunning {
			continue
		}
		if sb.NodeID == "" {
			continue
		}
		if _, dead := evictable[sb.NodeID]; !dead {
			continue
		}

		cur := byID[sb.ID]
		if cur == nil {
			continue
		}

		if len(cur.Spec.Mounts) > 0 {
			cur.Status = sandbox.Status{
				Phase:     sandbox.PhaseFailed,
				Message:   fmt.Sprintf("node %s heartbeat stale; host mounts cannot be rescheduled", shortNode(sb.NodeID)),
				UpdatedAt: now,
			}
			cur.UpdatedAt = now
			out = append(out, EvictDecision{
				Sandbox:      cur,
				SourceNodeID: sb.NodeID,
				FailedMount:  true,
			})
			continue
		}

		excludeAll := copyExclude(exclude)
		excludeAll[sb.NodeID] = struct{}{}
		if !HasLiveSchedulableNode(nodes, now, excludeAll) {
			continue
		}
		target := SelectNodeOpts(nodes, working, now, SelectOpts{ExcludeNodeIDs: excludeAll})
		if target == "" || target == sb.NodeID {
			continue
		}

		oldNode := sb.NodeID
		cur.NodeID = target
		if cur.AssignmentGeneration < 1 {
			cur.AssignmentGeneration = 1
		}
		cur.AssignmentGeneration++
		cur.Status = sandbox.Status{
			Phase:     sandbox.PhasePending,
			Message:   fmt.Sprintf("rescheduled: node %s heartbeat stale", shortNode(oldNode)),
			UpdatedAt: now,
		}
		cur.UpdatedAt = now
		out = append(out, EvictDecision{
			Sandbox:      cur,
			SourceNodeID: oldNode,
		})
	}
	return out
}

func copyExclude(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in)+1)
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func shortNode(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
