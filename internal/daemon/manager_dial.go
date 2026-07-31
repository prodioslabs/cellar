package daemon

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/prodioslabs/cellar/internal/grpcapi"
)

// managerDialAddr returns the preferred manager gRPC address.
func (d *Daemon) managerDialAddr() string {
	addrs := d.managerDialAddrs()
	if len(addrs) == 0 {
		return ""
	}
	return addrs[0]
}

// managerDialAddrs returns ordered manager gRPC addresses for failover.
// Raft nodes prefer LeaderGRPC; workers prefer the sticky ManagerAddr (often
// the last known leader), then the rediscovered ManagerAddrs list.
func (d *Daemon) managerDialAddrs() []string {
	d.mu.Lock()
	raft := d.raft
	d.mu.Unlock()

	state := d.idStore.State()
	var prefer string
	if raft != nil {
		if a := raft.LeaderGRPC(); a != "" {
			prefer = a
		} else if a := raft.GRPCAdvertise(); a != "" {
			prefer = a
		}
	}
	if prefer == "" {
		prefer = state.ManagerAddr
	}
	if prefer == "" {
		prefer = state.AdvertiseAddr
	}
	return grpcapi.MergeManagerAddrs(prefer, state.ManagerAddrs, []string{state.ManagerAddr, state.AdvertiseAddr})
}

// applyManagerEndpoints persists leader_grpc / manager_addrs from control-plane responses.
func (d *Daemon) applyManagerEndpoints(leader string, addrs []string) {
	if leader == "" && len(addrs) == 0 {
		return
	}
	state := d.idStore.State()
	newState := state
	changed := false
	if leader != "" && newState.ManagerAddr != leader {
		newState.ManagerAddr = leader
		changed = true
	}
	merged := grpcapi.MergeManagerAddrs(newState.ManagerAddr, addrs, state.ManagerAddrs)
	if !stringSlicesEqual(merged, newState.ManagerAddrs) {
		newState.ManagerAddrs = merged
		changed = true
	}
	if !changed {
		return
	}
	if err := d.idStore.SaveState(newState); err != nil {
		return
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func seedManagerAddrs(remoteAddr, leader string, fromIssue []string) (prefer string, all []string) {
	prefer = remoteAddr
	if leader != "" {
		prefer = leader
	}
	all = grpcapi.MergeManagerAddrs(prefer, []string{remoteAddr}, fromIssue)
	return prefer, all
}

func isRetryableManagerErr(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return true // dial / transport errors
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

// forEachManager tries fn against each known manager address until one succeeds
// or a non-retryable error is returned.
func (d *Daemon) forEachManager(fn func(addr string) error) error {
	addrs := d.managerDialAddrs()
	if len(addrs) == 0 {
		return fmt.Errorf("no manager address")
	}
	var lastErr error
	for _, addr := range addrs {
		err := fn(addr)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableManagerErr(err) {
			return err
		}
	}
	return lastErr
}
