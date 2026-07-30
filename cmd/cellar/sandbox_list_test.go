package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

func TestParseSandboxFilters(t *testing.T) {
	f, err := parseSandboxFilters([]string{
		"phase=running",
		"phase=failed",
		"desired=running",
		"image=alpine",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.phases) != 2 || f.phases[0] != "running" || f.phases[1] != "failed" {
		t.Fatalf("phases: %#v", f.phases)
	}
	if len(f.desireds) != 1 || f.desireds[0] != "running" {
		t.Fatalf("desireds: %#v", f.desireds)
	}
	if len(f.images) != 1 || f.images[0] != "alpine" {
		t.Fatalf("images: %#v", f.images)
	}
}

func TestParseSandboxFiltersErrors(t *testing.T) {
	cases := []string{"", "phase", "=running", "phase=", "foo=bar"}
	for _, c := range cases {
		if _, err := parseSandboxFilters([]string{c}); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestApplySandboxFiltersANDOR(t *testing.T) {
	sandboxes := []*cellarv1.Sandbox{
		{
			Id:           "a",
			DesiredState: "running",
			Spec:         &cellarv1.SandboxSpec{Image: "alpine"},
			Status:       &cellarv1.SandboxStatus{Phase: "running"},
		},
		{
			Id:           "b",
			DesiredState: "running",
			Spec:         &cellarv1.SandboxSpec{Image: "alpine"},
			Status:       &cellarv1.SandboxStatus{Phase: "failed"},
		},
		{
			Id:           "c",
			DesiredState: "stopped",
			Spec:         &cellarv1.SandboxSpec{Image: "nginx"},
			Status:       &cellarv1.SandboxStatus{Phase: "running"},
		},
		{
			Id:           "d",
			DesiredState: "running",
			Spec:         &cellarv1.SandboxSpec{Image: "busybox"},
			Status:       &cellarv1.SandboxStatus{Phase: "pending"},
		},
	}

	// phase OR (running|failed) AND desired=running AND image=alpine
	got := applySandboxFilters(sandboxes, sandboxListFilter{
		phases:   []string{"running", "failed"},
		desireds: []string{"running"},
		images:   []string{"alpine"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d sandboxes, want 2", len(got))
	}
	if got[0].Id != "a" || got[1].Id != "b" {
		t.Fatalf("got ids %s,%s want a,b", got[0].Id, got[1].Id)
	}
}

func TestFilterSandboxesByNode(t *testing.T) {
	sandboxes := []*cellarv1.Sandbox{
		{Id: "a", NodeId: "node-aaa"},
		{Id: "b", NodeId: "node-bbb"},
		{Id: "c", NodeId: "node-aaa"},
		{Id: "d", NodeId: ""},
	}
	got := filterSandboxesByNode(sandboxes, "node-aaa")
	if len(got) != 2 || got[0].Id != "a" || got[1].Id != "c" {
		t.Fatalf("unexpected: %#v", got)
	}
	all := filterSandboxesByNode(sandboxes, "")
	if len(all) != 4 {
		t.Fatalf("empty node filter should keep all, got %d", len(all))
	}
}

func TestResolveNodeIDPrefix(t *testing.T) {
	nodes := []*cellarv1.NodeInfo{
		{NodeId: "abcdef0123456789"},
		{NodeId: "abcdef9999999999"},
		{NodeId: "deadbeef00000000"},
	}

	id, err := resolveNodeIDPrefix(nodes, "abcdef0123456789")
	if err != nil || id != "abcdef0123456789" {
		t.Fatalf("exact: got %q err=%v", id, err)
	}

	id, err = resolveNodeIDPrefix(nodes, "dead")
	if err != nil || id != "deadbeef00000000" {
		t.Fatalf("prefix: got %q err=%v", id, err)
	}

	if _, err := resolveNodeIDPrefix(nodes, "abcdef"); err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
	if _, err := resolveNodeIDPrefix(nodes, "zzzz"); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestWriteSandboxTable(t *testing.T) {
	var buf bytes.Buffer
	err := writeSandboxTable(&buf, []*cellarv1.Sandbox{
		{
			Id:           "sb-1",
			NodeId:       "0123456789abcdef",
			DesiredState: "running",
			Spec:         &cellarv1.SandboxSpec{Image: "alpine"},
			Status: &cellarv1.SandboxStatus{
				Phase:       "running",
				ContainerId: "containerabcdef",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "ID") || !strings.Contains(out, "NODE") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "0123456789ab") {
		t.Fatalf("expected truncated node id: %q", out)
	}
	if strings.Contains(out, "0123456789abcdef") {
		t.Fatalf("node id should be truncated: %q", out)
	}
	if !strings.Contains(out, "containerabc") {
		t.Fatalf("expected truncated container id: %q", out)
	}
	if strings.Contains(out, "containerabcdef") {
		t.Fatalf("container id should be truncated: %q", out)
	}
}

func TestSandboxListJSONIsTopLevelArray(t *testing.T) {
	sandboxes := []*cellarv1.Sandbox{
		{
			Id:           "sb-1",
			NodeId:       "node-1",
			DesiredState: "running",
			Spec:         &cellarv1.SandboxSpec{Image: "alpine"},
			Status:       &cellarv1.SandboxStatus{Phase: "running", ContainerId: "c1"},
		},
	}
	b, err := sandboxListToJSONArray(sandboxes)
	if err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("expected top-level JSON array: %v\n%s", err, b)
	}
	if len(arr) != 1 {
		t.Fatalf("len=%d", len(arr))
	}
	if arr[0]["id"] != "sb-1" {
		t.Fatalf("id: %#v", arr[0]["id"])
	}
	if arr[0]["nodeId"] != "node-1" {
		t.Fatalf("expected camelCase nodeId, got %#v", arr[0])
	}
	// Must not be wrapped.
	var wrapped map[string]any
	if err := json.Unmarshal(b, &wrapped); err == nil {
		t.Fatalf("JSON should be an array, not an object: %#v", wrapped)
	}

	empty, err := sandboxListToJSONArray(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(empty)) != "[]" {
		t.Fatalf("nil list should encode as [], got %q", empty)
	}
}

func TestSandboxListYAMLIsTopLevelSequence(t *testing.T) {
	sandboxes := []*cellarv1.Sandbox{
		{
			Id:           "sb-1",
			NodeId:       "node-1",
			DesiredState: "running",
			Spec:         &cellarv1.SandboxSpec{Image: "alpine"},
			Status:       &cellarv1.SandboxStatus{Phase: "running"},
		},
	}
	b, err := sandboxListToYAML(sandboxes)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if !strings.HasPrefix(strings.TrimSpace(out), "-") {
		t.Fatalf("expected YAML sequence, got:\n%s", out)
	}
	if strings.Contains(out, "sandboxes:") {
		t.Fatalf("should not wrap under sandboxes:\n%s", out)
	}
	if !strings.Contains(out, "nodeId:") {
		t.Fatalf("expected camelCase nodeId:\n%s", out)
	}
	if !strings.Contains(out, "id: sb-1") {
		t.Fatalf("missing id:\n%s", out)
	}

	empty, err := sandboxListToYAML(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(empty)) != "[]" {
		t.Fatalf("nil list should encode as [], got %q", empty)
	}
}

func TestWriteSandboxListFormatValidation(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSandboxList(&buf, "xml", nil); err == nil {
		t.Fatal("expected unsupported format error")
	}
}
