package egress_test

import (
	"encoding/binary"
	"testing"

	"github.com/prodioslabs/cellar/internal/egress"
)

func TestBuildDNSAResponseTTL(t *testing.T) {
	// Minimal DNS query for example.com A
	q := []byte{
		0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm',
		0x00,
		0x00, 0x01, 0x00, 0x01,
	}
	ip := []byte{172, 30, 0, 2}
	out, err := egress.BuildDNSAResponse(q, ip)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < len(q)+16 {
		t.Fatalf("short response %d", len(out))
	}
	// TTL at answer offset: question end + 6
	ttl := binary.BigEndian.Uint32(out[len(q)+6 : len(q)+10])
	if ttl != 10 {
		t.Fatalf("ttl=%d want 10", ttl)
	}
}

func TestBuildDNSNXDomain(t *testing.T) {
	q := []byte{
		0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x06, 'g', 'o', 'o', 'g', 'l', 'e',
		0x03, 'c', 'o', 'm',
		0x00,
		0x00, 0x01, 0x00, 0x01,
	}
	out, err := egress.BuildDNSNXDomain(q)
	if err != nil {
		t.Fatal(err)
	}
	if out[3]&0x0f != 3 {
		t.Fatalf("rcode=%d want NXDOMAIN(3)", out[3]&0x0f)
	}
}
