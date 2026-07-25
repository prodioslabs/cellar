package egress

import (
	"fmt"
	"net"
)

// DeniedCIDRs are destinations a sandbox may never reach, regardless of its
// policy. They cover the host's own loopback, RFC 1918 / CGNAT space, and
// link-local (which includes cloud metadata at 169.254.169.254).
//
// Operators who genuinely need a private destination carve exceptions out at
// the node level; a sandbox spec can never widen this list.
var DeniedCIDRs = []string{
	// IPv4
	"10.0.0.0/8",     // RFC 1918 private
	"100.64.0.0/10",  // RFC 6598 CGNAT / shared address space
	"127.0.0.0/8",    // RFC 1122 loopback
	"169.254.0.0/16", // RFC 3927 link-local, incl. cloud metadata
	"172.16.0.0/12",  // RFC 1918 private
	"192.168.0.0/16", // RFC 1918 private
	// IPv6
	"::1/128",   // RFC 4291 loopback
	"fc00::/7",  // RFC 4193 unique local
	"fe80::/10", // RFC 4291 link-local
}

var parsedDeniedCIDRs = mustParseCIDRs(DeniedCIDRs)

// IsDenied reports whether ip falls into a always-denied range.
func IsDenied(ip net.IP) bool {
	return cidrsContain(parsedDeniedCIDRs, ip)
}

// ParseCIDRs parses a list of CIDR strings.
func ParseCIDRs(list []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(list))
	for _, c := range list {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("invalid cidr %q: %w", c, err)
		}
		out = append(out, n)
	}
	return out, nil
}

func mustParseCIDRs(list []string) []*net.IPNet {
	out, err := ParseCIDRs(list)
	if err != nil {
		panic(err)
	}
	return out
}

func cidrsContain(nets []*net.IPNet, ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
