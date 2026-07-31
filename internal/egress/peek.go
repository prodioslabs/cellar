package egress

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"strings"
)

// peekBufSize holds a full-size TLS record (16 KiB) plus its 5-byte header, so
// a ClientHello never exceeds what bufio can buffer.
const peekBufSize = 5 + (1 << 14) + 512

// maxHTTPHeader bounds the request head we are willing to buffer.
const maxHTTPHeader = 8 << 10

var (
	errNotTLS  = errors.New("not a tls client hello")
	errNotHTTP = errors.New("not an http request")
	errNoName  = errors.New("no hostname")
	errShort   = errors.New("truncated")
)

// PeekConn hands buffered bytes back to the upstream splice after inspection.
type PeekConn struct {
	net.Conn
	R *bufio.Reader
}

// NewPeekConn wraps c with a buffered reader for SNI/Host peeks.
func NewPeekConn(c net.Conn) *PeekConn {
	return &PeekConn{Conn: c, R: bufio.NewReaderSize(c, peekBufSize)}
}

func (c *PeekConn) Read(b []byte) (int, error) { return c.R.Read(b) }

// PeekTLSSNI extracts the server_name from a TLS ClientHello without consuming it.
func PeekTLSSNI(br *bufio.Reader) (string, error) {
	hdr, err := br.Peek(5)
	if err != nil {
		return "", err
	}
	if hdr[0] != 0x16 {
		return "", errNotTLS
	}
	recLen := int(binary.BigEndian.Uint16(hdr[3:5]))
	if recLen < 4 || 5+recLen > peekBufSize {
		return "", errNotTLS
	}
	buf, err := br.Peek(5 + recLen)
	if err != nil && len(buf) <= 5 {
		return "", err
	}
	return parseClientHelloSNI(buf[5:])
}

func parseClientHelloSNI(b []byte) (string, error) {
	if len(b) < 4 || b[0] != 0x01 {
		return "", errNotTLS
	}
	hsLen := int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	p := b[4:]
	if hsLen < len(p) {
		p = p[:hsLen]
	}
	// client_version(2) + random(32)
	if len(p) < 34 {
		return "", errShort
	}
	p = p[34:]

	// session_id
	if len(p) < 1 {
		return "", errShort
	}
	n := int(p[0])
	if p = p[1:]; len(p) < n {
		return "", errShort
	}
	p = p[n:]

	// cipher_suites
	if len(p) < 2 {
		return "", errShort
	}
	n = int(binary.BigEndian.Uint16(p))
	if p = p[2:]; len(p) < n {
		return "", errShort
	}
	p = p[n:]

	// compression_methods
	if len(p) < 1 {
		return "", errShort
	}
	n = int(p[0])
	if p = p[1:]; len(p) < n {
		return "", errShort
	}
	p = p[n:]

	// extensions
	if len(p) < 2 {
		return "", errNoName
	}
	extLen := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if len(p) > extLen {
		p = p[:extLen]
	}
	for len(p) >= 4 {
		typ := binary.BigEndian.Uint16(p)
		l := int(binary.BigEndian.Uint16(p[2:]))
		p = p[4:]
		if len(p) < l {
			return "", errShort
		}
		if typ == 0 {
			return parseSNIExtension(p[:l])
		}
		p = p[l:]
	}
	return "", errNoName
}

func parseSNIExtension(b []byte) (string, error) {
	if len(b) < 2 {
		return "", errShort
	}
	listLen := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if len(b) > listLen {
		b = b[:listLen]
	}
	for len(b) >= 3 {
		nameType := b[0]
		l := int(binary.BigEndian.Uint16(b[1:]))
		b = b[3:]
		if len(b) < l {
			return "", errShort
		}
		if nameType == 0 { // host_name
			return string(b[:l]), nil
		}
		b = b[l:]
	}
	return "", errNoName
}

// PeekHTTPHost extracts the Host header from a request head without consuming it.
func PeekHTTPHost(br *bufio.Reader) (string, error) {
	head, err := peekHead(br, maxHTTPHeader)
	if len(head) == 0 {
		if err == nil {
			err = errShort
		}
		return "", err
	}
	return parseHTTPHost(head)
}

// peekHead buffers up to max bytes, stopping as soon as the header terminator
// appears so a small request is not delayed waiting for a fixed-size peek.
func peekHead(br *bufio.Reader, max int) ([]byte, error) {
	if _, err := br.Peek(1); err != nil {
		return nil, err
	}
	for {
		n := min(br.Buffered(), max)
		b, _ := br.Peek(n)
		if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
			return b[:i+4], nil
		}
		if n >= max {
			return b, nil
		}
		if _, err := br.Peek(n + 1); err != nil {
			return b, err
		}
	}
}

func parseHTTPHost(head []byte) (string, error) {
	lines := bytes.Split(head, []byte("\r\n"))
	if len(lines) == 0 || !bytes.Contains(lines[0], []byte("HTTP/")) {
		return "", errNotHTTP
	}
	for _, ln := range lines[1:] {
		if len(ln) == 0 {
			break
		}
		k, v, ok := bytes.Cut(ln, []byte(":"))
		if !ok || !strings.EqualFold(string(bytes.TrimSpace(k)), "host") {
			continue
		}
		return normalizeHostHeader(string(bytes.TrimSpace(v))), nil
	}
	return "", errNoName
}

func normalizeHostHeader(v string) string {
	if h, _, err := net.SplitHostPort(v); err == nil {
		return h
	}
	return strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
}

// SanitizeHostname drops guest-supplied names that are not plausible DNS names,
// so garbage never reaches policy matching or the resolver.
func SanitizeHostname(h string) string {
	h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
	if h == "" || len(h) > 253 {
		return ""
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '.' || c == '-' || c == ':': // ':' for IPv6 literals in SNI
		default:
			return ""
		}
	}
	return h
}
