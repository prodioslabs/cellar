package gateway

import (
	"testing"

	"github.com/prodioslabs/cellar/internal/egress"
)

func TestUpstreamDialAddr(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		ip       string
		port     uint32
		match    egress.MatchType
		want     string
		wantErr  bool
	}{
		{"domain", "api.example.com", "", 443, egress.MatchDomain, "api.example.com:443", false},
		{"domain empty host", "", "", 443, egress.MatchDomain, "", true},
		{"cidr", "", "1.1.1.1", 443, egress.MatchCIDR, "1.1.1.1:443", false},
		{"cidr other port", "", "8.8.8.8", 53, egress.MatchCIDR, "8.8.8.8:53", false},
		{"cidr empty ip", "", "", 443, egress.MatchCIDR, "", true},
		{"cidr invalid ip", "", "not-an-ip", 443, egress.MatchCIDR, "", true},
		{"match none", "", "1.1.1.1", 443, egress.MatchNone, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := upstreamDialAddr(tt.hostname, tt.ip, tt.port, tt.match)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
