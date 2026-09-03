package daemon

import (
	"context"
	"log"
	"time"

	"github.com/prodioslabs/cellar/internal/scheduler"
)

const evictionTick = 5 * time.Second

// evictionLoop runs only while this node holds Raft leadership. It vacates
// desired_state=running sandboxes from heartbeat-stale, draining, or deleted nodes.
func (d *Daemon) evictionLoop(ctx context.Context) {
	ticker := time.NewTicker(evictionTick)
	defer ticker.Stop()
	for {
		d.runEviction(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) runEviction(ctx context.Context) {
	d.mu.Lock()
	raft := d.raft
	sbSrv := d.sandboxServer
	d.mu.Unlock()
	if raft == nil || !raft.IsLeader() || sbSrv == nil {
		return
	}
	q := sbSrv.Quarantine()

	nodes, err := raft.ListNodes(ctx)
	if err != nil {
		log.Printf("eviction: list nodes: %v", err)
		return
	}
	sandboxes, err := raft.ListSandboxes(ctx)
	if err != nil {
		log.Printf("eviction: list sandboxes: %v", err)
		return
	}

	now := time.Now().UTC()
	decisions := scheduler.PlanVacate(nodes, sandboxes, now, q.Excluded())
	if len(decisions) == 0 {
		return
	}

	marked := map[string]struct{}{}
	for _, dec := range decisions {
		if dec.Sandbox == nil {
			continue
		}
		prefix := vacateLogPrefix(dec.Reason)
		if err := raft.SaveSandbox(ctx, dec.Sandbox); err != nil {
			log.Printf("%s: save sandbox %s: %v", prefix, shortID(dec.Sandbox.ID), err)
			continue
		}
		if dec.Reason == scheduler.ReasonHeartbeat {
			if _, ok := marked[dec.SourceNodeID]; !ok {
				q.MarkEvicted(dec.SourceNodeID)
				marked[dec.SourceNodeID] = struct{}{}
			}
		}
		if dec.FailedMount {
			log.Printf("%s: sandbox %s failed (host mounts) on node %s",
				prefix, shortID(dec.Sandbox.ID), shortID(dec.SourceNodeID))
			continue
		}
		log.Printf("%s: sandbox %s %s -> %s gen=%d",
			prefix, shortID(dec.Sandbox.ID), shortID(dec.SourceNodeID), shortID(dec.Sandbox.NodeID),
			dec.Sandbox.AssignmentGeneration)
	}
}

func vacateLogPrefix(reason scheduler.VacateReason) string {
	switch reason {
	case scheduler.ReasonDrain:
		return "drain"
	case scheduler.ReasonGone:
		return "gone"
	default:
		return "eviction"
	}
}
