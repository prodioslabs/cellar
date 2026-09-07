package daemon

import "testing"

func TestDefaultAdvertiseEmptyHost(t *testing.T) {
	const want = "127.0.0.1:17946"
	if got := defaultAdvertise(":17946"); got != want {
		t.Fatalf("defaultAdvertise(:17946) = %q, want %q", got, want)
	}
}

func TestDefaultAdvertisePreservesHost(t *testing.T) {
	const want = "192.0.2.10:17946"
	if got := defaultAdvertise(want); got != want {
		t.Fatalf("defaultAdvertise(%q) = %q, want %q", want, got, want)
	}
}

func TestDefaultRaftAddrEmptyHost(t *testing.T) {
	const want = "127.0.0.1:17947"
	if got := defaultRaftAddr(":17947"); got != want {
		t.Fatalf("defaultRaftAddr(:17947) = %q, want %q", got, want)
	}
}

func TestDefaultRaftAddrPreservesHost(t *testing.T) {
	const want = "192.0.2.10:17947"
	if got := defaultRaftAddr(want); got != want {
		t.Fatalf("defaultRaftAddr(%q) = %q, want %q", want, got, want)
	}
}
