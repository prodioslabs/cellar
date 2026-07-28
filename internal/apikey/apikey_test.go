package apikey

import (
	"strings"
	"testing"
)

func TestGenerateHashMask(t *testing.T) {
	g, err := Generate("ci")
	if err != nil {
		t.Fatal(err)
	}
	if g.Raw == "" || !strings.HasPrefix(g.Raw, Prefix) {
		t.Fatalf("raw=%q", g.Raw)
	}
	if g.Key.KeyHash != Hash(g.Raw) {
		t.Fatal("hash mismatch")
	}
	if g.Key.Mask != Mask(g.Raw) {
		t.Fatalf("mask=%q want=%q", g.Key.Mask, Mask(g.Raw))
	}
	if _, err := ParseRaw(g.Raw); err != nil {
		t.Fatal(err)
	}
	if EqualHash(g.Key.KeyHash, Hash("cellar_0000000000000000000000000000000000000000")) {
		t.Fatal("unexpected equal")
	}
	if !strings.HasPrefix(g.Key.Mask, Prefix) {
		t.Fatalf("mask=%q", g.Key.Mask)
	}
}
