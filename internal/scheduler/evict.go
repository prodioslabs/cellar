package scheduler

import (
	"fmt"
	"time"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

// VacateReason explains why a sandbox was evacuated.
type VacateReason string

const (
	ReasonHeartbeat VacateReason = "heartbeat"
	ReasonDrain     VacateReason = "drain"
	ReasonGone      VacateReason = "gone"
)

// EvictDecision is a sandbox mutation produced by PlanVacate.
type EvictDecision struct {
	Sandbox *sandbox.Sandbox
	// SourceNodeID is the node the sandbox was taken from (or left on for FailedMount).
	SourceNodeID string
	// FailedMount is true when the sandbox was marked failed due to host mounts
	// (not reassigned).
	FailedMount bool
	// Reason is why this sandbox was vacated.
	Reason VacateReason
}

// PlanVacate returns sandbox updates for three source kinds:
//   - gone: NodeID not present in nodes (leave / node rm without drain)
//   - heartbeat: node exists but heartbeat older than HeartbeatEvictAfter
//   - drain: node exists, live, availability=drain
//
// Running sandboxes without host mounts are reassigned onto live schedulable
// nodes (spread). Host mounts: failed for gone/heartbeat; left in place for
// drain. Stopped/removed sandboxes are ignored.
//
// exclude lists node IDs that must not receive placements (quarantine).
// Callers should MarkEvicted only for ReasonHeartbeat SourceNodeIDs.
func PlanVacate(nodes []*node.Node, sandboxes []*sandbox.Sandbox, now time.Time, exclude map[string]struct{}) []EvictDecision {
	if len(sandboxes) == 0 {
		return nil
	}

	byNode := make(map[string]*node.Node, len(nodes))
	for _, n := range nodes {
		if n == nil || n.ID == "" {
			continue
		}
		byNode[n.ID] = n
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

		reason, ok := classifyVacate(byNode, sb.NodeID, now)
		if !ok {
			continue
		}

		cur := byID[sb.ID]
		if cur == nil {
			continue
		}

		if cur.Spec.HasHostMounts() {
			if reason == ReasonDrain {
				// Node is still live; leave bind-mounted sandboxes in place.
				continue
			}
			cur.Status = sandbox.Status{
				Phase:     sandbox.PhaseFailed,
				Message:   mountFailMessage(reason, sb.NodeID),
				UpdatedAt: now,
			}
			cur.UpdatedAt = now
			out = append(out, EvictDecision{
				Sandbox:      cur,
				SourceNodeID: sb.NodeID,
				FailedMount:  true,
				Reason:       reason,
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
			Message:   rescheduleMessage(reason, oldNode),
			UpdatedAt: now,
		}
		cur.UpdatedAt = now
		out = append(out, EvictDecision{
			Sandbox:      cur,
			SourceNodeID: oldNode,
			Reason:       reason,
		})
	}
	return out
}

// classifyVacate returns the vacate reason for a sandbox's NodeID.
// Heartbeat wins over drain when the node record still exists.
func classifyVacate(byNode map[string]*node.Node, nodeID string, now time.Time) (VacateReason, bool) {
	n, ok := byNode[nodeID]
	if !ok {
		return ReasonGone, true
	}
	if IsNodeEvictable(n, now) {
		return ReasonHeartbeat, true
	}
	if n.Availability.Effective() == node.AvailabilityDrain {
		return ReasonDrain, true
	}
	return "", false
}

func mountFailMessage(reason VacateReason, nodeID string) string {
	switch reason {
	case ReasonGone:
		return fmt.Sprintf("node %s left cluster; host mounts cannot be rescheduled", shortNode(nodeID))
	default:
		return fmt.Sprintf("node %s heartbeat stale; host mounts cannot be rescheduled", shortNode(nodeID))
	}
}

func rescheduleMessage(reason VacateReason, nodeID string) string {
	switch reason {
	case ReasonDrain:
		return fmt.Sprintf("rescheduled: node %s draining", shortNode(nodeID))
	case ReasonGone:
		return fmt.Sprintf("rescheduled: node %s left cluster", shortNode(nodeID))
	default:
		return fmt.Sprintf("rescheduled: node %s heartbeat stale", shortNode(nodeID))
	}
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
