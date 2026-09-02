package scheduler

import "sync"

// Quarantine tracks nodes that were recently evacuated so they do not
// immediately receive new placements while flapping.
type Quarantine struct {
	mu    sync.Mutex
	nodes map[string]int // nodeID → consecutive successful heartbeats since MarkEvicted
}

// NewQuarantine returns an empty quarantine tracker.
func NewQuarantine() *Quarantine {
	return &Quarantine{nodes: make(map[string]int)}
}

// MarkEvicted starts (or resets) quarantine for nodeID.
func (q *Quarantine) MarkEvicted(nodeID string) {
	if q == nil || nodeID == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nodes[nodeID] = 0
}

// NoteHeartbeat records a successful heartbeat. After QuarantineHeartbeats
// consecutive notes, the node leaves quarantine.
func (q *Quarantine) NoteHeartbeat(nodeID string) {
	if q == nil || nodeID == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	n, ok := q.nodes[nodeID]
	if !ok {
		return
	}
	n++
	if n >= QuarantineHeartbeats {
		delete(q.nodes, nodeID)
		return
	}
	q.nodes[nodeID] = n
}

// Excluded returns a copy of node IDs currently quarantined.
func (q *Quarantine) Excluded() map[string]struct{} {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.nodes) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(q.nodes))
	for id := range q.nodes {
		out[id] = struct{}{}
	}
	return out
}

// Contains reports whether nodeID is quarantined.
func (q *Quarantine) Contains(nodeID string) bool {
	if q == nil || nodeID == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.nodes[nodeID]
	return ok
}
