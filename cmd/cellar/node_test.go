package main

import (
	"testing"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/node"
)

func TestSandboxCountDisplay(t *testing.T) {
	tests := []struct {
		name string
		n    *cellarv1.NodeInfo
		want string
	}{
		{name: "nil", n: nil, want: "-"},
		{name: "down with zero count", n: &cellarv1.NodeInfo{Status: string(node.StatusDown), RuntimeSandboxCount: 0}, want: "-"},
		{name: "down with stale count", n: &cellarv1.NodeInfo{Status: string(node.StatusDown), RuntimeSandboxCount: 4}, want: "-"},
		{name: "ready empty", n: &cellarv1.NodeInfo{Status: string(node.StatusReady), RuntimeSandboxCount: 0}, want: "0"},
		{name: "ready with sandboxes", n: &cellarv1.NodeInfo{Status: string(node.StatusReady), RuntimeSandboxCount: 3}, want: "3"},
		{name: "empty status", n: &cellarv1.NodeInfo{RuntimeSandboxCount: 2}, want: "-"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxCountDisplay(tc.n); got != tc.want {
				t.Fatalf("sandboxCountDisplay() = %q, want %q", got, tc.want)
			}
		})
	}
}
