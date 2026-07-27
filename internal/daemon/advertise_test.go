package daemon

import (
	"net"
	"strings"
	"testing"
)

func TestDefaultAdvertiseEmptyHost(t *testing.T) {
	got := defaultAdvertise(":17946")
	host, port, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatalf("defaultAdvertise(:17946) = %q: %v", got, err)
	}
	if port != "17946" {
		t.Fatalf("port = %q, want 17946", port)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		t.Fatalf("host = %q, want IPv4", host)
	}
	private := privateIPv4()
	if host != private {
		t.Fatalf("host = %q, want privateIPv4() %q", host, private)
	}
	if private != "127.0.0.1" && host == "127.0.0.1" {
		t.Fatal("used 127.0.0.1 despite a private address being available")
	}
}

func TestDefaultAdvertisePreservesHost(t *testing.T) {
	const want = "192.0.2.10:17946"
	if got := defaultAdvertise(want); got != want {
		t.Fatalf("defaultAdvertise(%q) = %q, want %q", want, got, want)
	}
}

func TestPrivateIPv4(t *testing.T) {
	got := privateIPv4()
	ip := net.ParseIP(got)
	if ip == nil || ip.To4() == nil {
		t.Fatalf("privateIPv4() = %q, want IPv4", got)
	}
	if !ip.IsLoopback() && !ip.IsPrivate() {
		t.Fatalf("privateIPv4() = %q, want private or loopback fallback", got)
	}
	if strings.Contains(got, ":") {
		t.Fatalf("privateIPv4() = %q, want bare IPv4", got)
	}
}
