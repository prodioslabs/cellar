package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

var sandboxListProtoJSON = protojson.MarshalOptions{
	UseProtoNames:   false, // camelCase for Nushell / JS tooling
	EmitUnpopulated: false,
}

// sandboxListFilter holds parsed --filter key=value pairs.
// Different keys are AND-combined; multiple values for one key are OR-combined.
type sandboxListFilter struct {
	phases   []string
	desireds []string
	images   []string
}

func parseSandboxFilters(raw []string) (sandboxListFilter, error) {
	var f sandboxListFilter
	for _, r := range raw {
		key, value, ok := strings.Cut(r, "=")
		if !ok || key == "" || value == "" {
			return f, fmt.Errorf("invalid --filter %q (want key=value)", r)
		}
		switch key {
		case "phase":
			f.phases = append(f.phases, value)
		case "desired":
			f.desireds = append(f.desireds, value)
		case "image":
			f.images = append(f.images, value)
		default:
			return f, fmt.Errorf("unknown --filter key %q (supported: phase, desired, image)", key)
		}
	}
	return f, nil
}

func matchAny(want []string, got string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if w == got {
			return true
		}
	}
	return false
}

func sandboxImageRef(sb *cellarv1.Sandbox) string {
	if sb == nil {
		return ""
	}
	return sandbox.FromProto(sb).Spec.ImageReference()
}

func applySandboxFilters(sandboxes []*cellarv1.Sandbox, f sandboxListFilter) []*cellarv1.Sandbox {
	if len(f.phases) == 0 && len(f.desireds) == 0 && len(f.images) == 0 {
		return sandboxes
	}
	out := make([]*cellarv1.Sandbox, 0, len(sandboxes))
	for _, sb := range sandboxes {
		if !matchAny(f.phases, sb.GetStatus().GetPhase()) {
			continue
		}
		if !matchAny(f.desireds, sb.GetDesiredState()) {
			continue
		}
		if !matchAny(f.images, sandboxImageRef(sb)) {
			continue
		}
		out = append(out, sb)
	}
	return out
}

func filterSandboxesByNode(sandboxes []*cellarv1.Sandbox, nodeID string) []*cellarv1.Sandbox {
	if nodeID == "" {
		return sandboxes
	}
	out := make([]*cellarv1.Sandbox, 0)
	for _, sb := range sandboxes {
		if sb.GetNodeId() == nodeID {
			out = append(out, sb)
		}
	}
	return out
}

// resolveNodeIDPrefix resolves an exact or unambiguous node id prefix from a NodeList.
func resolveNodeIDPrefix(nodes []*cellarv1.NodeInfo, idOrPrefix string) (string, error) {
	if idOrPrefix == "" {
		return "", fmt.Errorf("node id is required")
	}
	for _, n := range nodes {
		if n.GetNodeId() == idOrPrefix {
			return n.GetNodeId(), nil
		}
	}
	var matches []string
	for _, n := range nodes {
		if strings.HasPrefix(n.GetNodeId(), idOrPrefix) {
			matches = append(matches, n.GetNodeId())
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("node %q not found", idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("node id prefix %q is ambiguous (%d matches)", idOrPrefix, len(matches))
	}
}

func writeSandboxTable(w io.Writer, sandboxes []*cellarv1.Sandbox) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tNODE\tDESIRED\tPHASE\tIMAGE")
	for _, sb := range sandboxes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			sb.GetId(),
			sb.GetName(),
			shortNodeID(sb.GetNodeId()),
			sb.GetDesiredState(),
			sb.GetStatus().GetPhase(),
			sandboxImageRef(sb),
		)
	}
	return tw.Flush()
}

func sandboxListToJSONArray(sandboxes []*cellarv1.Sandbox) ([]byte, error) {
	if sandboxes == nil {
		sandboxes = []*cellarv1.Sandbox{}
	}
	parts := make([]json.RawMessage, 0, len(sandboxes))
	for _, sb := range sandboxes {
		b, err := sandboxListProtoJSON.Marshal(sb)
		if err != nil {
			return nil, err
		}
		parts = append(parts, b)
	}
	out, err := json.MarshalIndent(parts, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func sandboxListToYAML(sandboxes []*cellarv1.Sandbox) ([]byte, error) {
	jsonBytes, err := sandboxListToJSONArray(sandboxes)
	if err != nil {
		return nil, err
	}
	var docs []any
	if err := json.Unmarshal(jsonBytes, &docs); err != nil {
		return nil, err
	}
	if docs == nil {
		docs = []any{}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(docs); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}

func writeSandboxList(w io.Writer, format string, sandboxes []*cellarv1.Sandbox) error {
	switch strings.ToLower(format) {
	case "table", "":
		return writeSandboxTable(w, sandboxes)
	case "json":
		b, err := sandboxListToJSONArray(sandboxes)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	case "yaml", "yml":
		b, err := sandboxListToYAML(sandboxes)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	default:
		return fmt.Errorf("unsupported --format %q (want table|json|yaml)", format)
	}
}
