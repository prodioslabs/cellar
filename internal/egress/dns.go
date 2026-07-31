package egress

import (
	"encoding/binary"
	"fmt"
)

const defaultDNSTTL = 10 // seconds; short so policy updates take effect quickly

// ParseDNSQName extracts the first question name from a DNS query packet.
func ParseDNSQName(pkt []byte) (string, error) {
	return parseDNSQName(pkt)
}

// parseDNSQName extracts the first question name from a DNS query packet.
func parseDNSQName(pkt []byte) (string, error) {
	if len(pkt) < 12 {
		return "", fmt.Errorf("short dns")
	}
	i := 12
	var labels []byte
	for i < len(pkt) {
		l := int(pkt[i])
		i++
		if l == 0 {
			break
		}
		if l > 63 || i+l > len(pkt) {
			return "", fmt.Errorf("bad label")
		}
		if len(labels) > 0 {
			labels = append(labels, '.')
		}
		labels = append(labels, pkt[i:i+l]...)
		i += l
	}
	return string(labels), nil
}

// parseDNSQType returns the QTYPE of the first question (1=A, 28=AAAA).
func parseDNSQType(pkt []byte) (uint16, error) {
	if len(pkt) < 12 {
		return 0, fmt.Errorf("short dns")
	}
	i := 12
	for i < len(pkt) {
		l := int(pkt[i])
		i++
		if l == 0 {
			break
		}
		if l > 63 || i+l > len(pkt) {
			return 0, fmt.Errorf("bad label")
		}
		i += l
	}
	if i+4 > len(pkt) {
		return 0, fmt.Errorf("truncated question")
	}
	return binary.BigEndian.Uint16(pkt[i : i+2]), nil
}

func questionEnd(query []byte) (int, error) {
	if len(query) < 12 {
		return 0, fmt.Errorf("short dns")
	}
	i := 12
	for i < len(query) {
		l := int(query[i])
		i++
		if l == 0 {
			break
		}
		i += l
	}
	if i+4 > len(query) {
		return 0, fmt.Errorf("truncated question")
	}
	return i + 4, nil
}

// BuildDNSAResponse builds a simple A response (TTL 10s) copying the query question.
func BuildDNSAResponse(query []byte, ip []byte) ([]byte, error) {
	return buildDNSAResponse(query, ip, defaultDNSTTL)
}

// buildDNSAResponse builds a simple A response copying the query header/question.
func buildDNSAResponse(query []byte, ip []byte, ttl uint32) ([]byte, error) {
	if len(query) < 12 || len(ip) != 4 {
		return nil, fmt.Errorf("bad input")
	}
	i, err := questionEnd(query)
	if err != nil {
		return nil, err
	}
	q := query[:i]

	out := make([]byte, 0, len(q)+16)
	out = append(out, q...)
	out[2] = 0x81
	out[3] = 0x80
	binary.BigEndian.PutUint16(out[6:8], 1) // ANCOUNT=1

	ans := make([]byte, 12)
	ans[0], ans[1] = 0xc0, 0x0c
	binary.BigEndian.PutUint16(ans[2:4], 1) // TYPE A
	binary.BigEndian.PutUint16(ans[4:6], 1) // CLASS IN
	binary.BigEndian.PutUint32(ans[6:10], ttl)
	binary.BigEndian.PutUint16(ans[10:12], 4)
	out = append(out, ans...)
	out = append(out, ip...)
	return out, nil
}

// BuildDNSNXDomain builds an NXDOMAIN response for the query.
func BuildDNSNXDomain(query []byte) ([]byte, error) {
	i, err := questionEnd(query)
	if err != nil {
		return nil, err
	}
	out := make([]byte, i)
	copy(out, query[:i])
	out[2] = 0x81
	out[3] = 0x83 // RA + NXDOMAIN (rcode=3)
	binary.BigEndian.PutUint16(out[6:8], 0)
	return out, nil
}
