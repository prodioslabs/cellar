package daemon

import (
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/node"
)

func TestNodeType(t *testing.T) {
	worker := &node.Node{ID: "w1", Role: node.RoleWorker}
	manager := &node.Node{ID: "m1", Role: node.RoleManager}
	leader := &node.Node{ID: "m2", Role: node.RoleManager}

	tests := []struct {
		name     string
		n        *node.Node
		leaderID string
		want     string
	}{
		{name: "worker", n: worker, leaderID: "m2", want: "worker"},
		{name: "leader", n: leader, leaderID: "m2", want: "leader"},
		{name: "non-leader manager", n: manager, leaderID: "m2", want: "manager"},
		{name: "manager no leader known", n: manager, leaderID: "", want: "manager"},
		{name: "nil node", n: nil, leaderID: "m2", want: "worker"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeType(tc.n, tc.leaderID); got != tc.want {
				t.Fatalf("nodeType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNodeToInfoNodeType(t *testing.T) {
	now := time.Now().UTC()
	n := &node.Node{
		ID:                  "m2",
		Role:                node.RoleManager,
		Membership:          node.MembershipAccepted,
		RuntimeHeartbeatAt:  now,
		RuntimeSandboxCount: 3,
	}
	info := nodeToInfo(n, "m2", map[string]struct{}{"m2": {}}, now)
	if info.NodeType != "leader" {
		t.Fatalf("NodeType = %q, want leader", info.NodeType)
	}
	if info.ManagerStatus != "leader" {
		t.Fatalf("ManagerStatus = %q, want leader", info.ManagerStatus)
	}
}
