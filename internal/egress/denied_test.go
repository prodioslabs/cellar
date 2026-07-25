package egress

import (
	"net"
	"testing"
)

func TestIsDenied(t *testing.T) {
	denied := []string{
		"10.0.0.1", "10.255.255.254",
		"100.64.0.1", "100.127.255.254", // CGNAT
		"127.0.0.1", "127.1.2.3",
		"169.254.169.254", // cloud metadata
		"172.16.0.1", "172.31.255.254",
		"192.168.0.1", "192.168.255.254",
		"::1", "fc00::1", "fd12:3456::1", "fe80::1",
	}
	for _, s := range denied {
		if !IsDenied(net.ParseIP(s)) {
			t.Errorf("IsDenied(%s) = false, want true", s)
		}
	}

	allowed := []string{
		"8.8.8.8", "93.184.216.34", "1.1.1.1",
		"172.15.255.254", "172.32.0.1", // just outside 172.16/12
		"100.63.255.254", "100.128.0.1", // just outside 100.64/10
		"192.167.255.254", "192.169.0.1",
		"2001:db8::1", "2606:4700::1111",
	}
	for _, s := range allowed {
		if IsDenied(net.ParseIP(s)) {
			t.Errorf("IsDenied(%s) = true, want false", s)
		}
	}

	if IsDenied(nil) {
		t.Error("IsDenied(nil) = true, want false")
	}
}

func TestIsDeniedIPv4MappedIPv6(t *testing.T) {
	// net.ParseIP normalizes ::ffff:10.0.0.1 to the 4-byte form, so an
	// IPv4-mapped destination is still caught by the IPv4 ranges.
	if !IsDenied(net.ParseIP("::ffff:10.0.0.1")) {
		t.Error("IPv4-mapped private address must be denied")
	}
}

func TestParseCIDRs(t *testing.T) {
	nets, err := ParseCIDRs([]string{"10.20.0.0/16", "2001:db8::/32"})
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}
	if len(nets) != 2 {
		t.Fatalf("got %d nets, want 2", len(nets))
	}
	if !cidrsContain(nets, net.ParseIP("10.20.1.1")) {
		t.Error("expected 10.20.1.1 to be contained")
	}
	if cidrsContain(nets, net.ParseIP("10.21.1.1")) {
		t.Error("did not expect 10.21.1.1 to be contained")
	}
	if _, err := ParseCIDRs([]string{"10.20.0.0"}); err == nil {
		t.Error("expected an error for a bare IP")
	}
}
