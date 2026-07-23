package egress

import (
	"encoding/binary"
	"fmt"
)

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

// buildDNSAResponse builds a simple A response copying the query header/question.
func buildDNSAResponse(query []byte, ip []byte) ([]byte, error) {
	if len(query) < 12 || len(ip) != 4 {
		return nil, fmt.Errorf("bad input")
	}
	// Find end of question section.
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
		return nil, fmt.Errorf("truncated question")
	}
	i += 4 // QTYPE QCLASS
	q := query[:i]

	out := make([]byte, 0, len(q)+16)
	out = append(out, q...)
	// header flags: response, recursion available, no error
	out[2] = 0x81
	out[3] = 0x80
	binary.BigEndian.PutUint16(out[6:8], 1) // ANCOUNT=1

	// Answer: pointer to name at offset 12, TYPE A, CLASS IN, TTL 60, RDLENGTH 4, RDATA
	ans := []byte{0xc0, 0x0c, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, 0x04}
	ans = append(ans, ip...)
	out = append(out, ans...)
	return out, nil
}
