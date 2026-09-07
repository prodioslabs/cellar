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

func TestNodeHostname(t *testing.T) {
	tests := []struct {
		name string
		n    *cellarv1.NodeInfo
		want string
	}{
		{name: "nil", n: nil, want: ""},
		{name: "from hostname field", n: &cellarv1.NodeInfo{Hostname: "10.0.0.5", RuntimeGrpcAddr: "192.0.2.1:1"}, want: "10.0.0.5"},
		{name: "from runtime addr", n: &cellarv1.NodeInfo{RuntimeGrpcAddr: "10.0.0.5:17946"}, want: "10.0.0.5"},
		{name: "bare runtime addr", n: &cellarv1.NodeInfo{RuntimeGrpcAddr: "10.0.0.5"}, want: "10.0.0.5"},
		{name: "empty", n: &cellarv1.NodeInfo{}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeHostname(tc.n); got != tc.want {
				t.Fatalf("nodeHostname() = %q, want %q", got, tc.want)
			}
		})
	}
}
