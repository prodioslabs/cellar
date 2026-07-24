package sandbox

import (
	"fmt"
	"sort"
	"strings"
)

// PresetImages maps language runtime IDs to Alpine-based container images.
// A sandbox specifies either a custom Image or one of these runtimes.
var PresetImages = map[string]string{
	"node-26":     "node:26-alpine",
	"bun-1.3":     "oven/bun:1.3-alpine",
	"python-3.13": "astral/uv:python3.13-alpine",
	"go-1.26":     "golang:1.26-alpine",
}

// KnownRuntimes returns preset runtime IDs in sorted order.
func KnownRuntimes() []string {
	out := make([]string, 0, len(PresetImages))
	for id := range PresetImages {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ResolveImage returns the image for a preset runtime ID.
func ResolveImage(runtime string) (string, error) {
	runtime = strings.TrimSpace(runtime)
	img, ok := PresetImages[runtime]
	if !ok {
		return "", fmt.Errorf("unknown runtime %q (want one of: %s)", runtime, strings.Join(KnownRuntimes(), ", "))
	}
	return img, nil
}
