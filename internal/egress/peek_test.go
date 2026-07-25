package egress

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
)

// captureClientHello produces a real ClientHello by starting a handshake
// against a connection that records the first flight and then hangs up.
func captureClientHello(t *testing.T, serverName string) []byte {
	t.Helper()
	client, server := net.Pipe()
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		chunk := make([]byte, 4096)
		for {
			n, err := server.Read(chunk)
			buf.Write(chunk[:n])
			// A ClientHello is a single record; its length prefix tells us
			// when we have all of it.
			if b := buf.Bytes(); len(b) >= 5 && len(b) >= 5+int(binary.BigEndian.Uint16(b[3:5])) {
				break
			}
			if err != nil {
				break
			}
		}
		_ = server.Close()
	}()
	_ = tls.Client(client, &tls.Config{ServerName: serverName}).Handshake()
	_ = client.Close()
	<-done
	if buf.Len() == 0 {
		t.Fatal("captured no ClientHello")
	}
	return buf.Bytes()
}

func readerFor(b []byte) *bufio.Reader {
	return bufio.NewReaderSize(bytes.NewReader(b), peekBufSize)
}

func TestPeekTLSSNI(t *testing.T) {
	hello := captureClientHello(t, "api.example.com")
	got, err := peekTLSSNI(readerFor(hello))
	if err != nil {
		t.Fatalf("peekTLSSNI: %v", err)
	}
	if got != "api.example.com" {
		t.Fatalf("got SNI %q, want api.example.com", got)
	}
}

func TestPeekTLSSNIRejectsNonTLS(t *testing.T) {
	for name, input := range map[string][]byte{
		"http":       []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"),
		"ssh banner": []byte("SSH-2.0-OpenSSH_9.6\r\n"),
		"garbage":    bytes.Repeat([]byte{0xff}, 64),
	} {
		if _, err := peekTLSSNI(readerFor(input)); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestPeekTLSSNITruncated(t *testing.T) {
	hello := captureClientHello(t, "api.example.com")
	for _, n := range []int{1, 4, 5, 20, len(hello) / 2, len(hello) - 1} {
		// Must not panic or block; either an error or a best-effort answer.
		_, _ = peekTLSSNI(readerFor(hello[:n]))
	}
}

func TestPeekTLSSNIWithoutServerName(t *testing.T) {
	// An IP-literal ServerName is dropped by crypto/tls, so this hello has no
	// server_name extension at all.
	hello := captureClientHello(t, "203.0.113.9")
	if _, err := peekTLSSNI(readerFor(hello)); err == nil {
		t.Fatal("expected an error when no server_name is present")
	}
}

func TestPeekTLSSNIDoesNotConsume(t *testing.T) {
	hello := captureClientHello(t, "api.example.com")
	br := readerFor(hello)
	if _, err := peekTLSSNI(br); err != nil {
		t.Fatalf("peekTLSSNI: %v", err)
	}
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(rest, hello) {
		t.Fatal("peeked bytes must still be readable by the upstream splice")
	}
}

func TestPeekHTTPHost(t *testing.T) {
	tests := []struct {
		name string
		req  string
		want string
	}{
		{"simple", "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n", "example.com"},
		{"with port", "GET / HTTP/1.1\r\nHost: example.com:8080\r\n\r\n", "example.com"},
		{"lowercase header", "GET / HTTP/1.1\r\nhost: example.com\r\n\r\n", "example.com"},
		{"padded value", "GET / HTTP/1.1\r\nHost:   example.com  \r\n\r\n", "example.com"},
		{"not first header", "POST /x HTTP/1.1\r\nUser-Agent: c\r\nHost: api.example.com\r\n\r\n", "api.example.com"},
		{"ipv6 literal", "GET / HTTP/1.1\r\nHost: [2001:db8::1]:80\r\n\r\n", "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := peekHTTPHost(readerFor([]byte(tt.req)))
			if err != nil {
				t.Fatalf("peekHTTPHost: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPeekHTTPHostFailures(t *testing.T) {
	for name, req := range map[string]string{
		"no host header": "GET / HTTP/1.1\r\nUser-Agent: c\r\n\r\n",
		"not http":       "\x16\x03\x01\x00\x05hello",
		"garbage":        "\x00\x01\x02\x03",
	} {
		if _, err := peekHTTPHost(readerFor([]byte(req))); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestPeekHTTPHostBounded(t *testing.T) {
	// Headers longer than the cap are abandoned rather than buffered forever.
	req := "GET / HTTP/1.1\r\nX-Pad: " + strings.Repeat("a", maxHTTPHeader*2) +
		"\r\nHost: example.com\r\n\r\n"
	if _, err := peekHTTPHost(readerFor([]byte(req))); err == nil {
		t.Fatal("expected an error for an oversized header block")
	}
}

func TestPeekHTTPHostDoesNotConsume(t *testing.T) {
	req := []byte("GET /path HTTP/1.1\r\nHost: example.com\r\n\r\nbody")
	br := readerFor(req)
	if _, err := peekHTTPHost(br); err != nil {
		t.Fatalf("peekHTTPHost: %v", err)
	}
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(rest, req) {
		t.Fatal("peeked bytes must still be readable by the upstream splice")
	}
}

func TestSanitizeHostname(t *testing.T) {
	tests := map[string]string{
		"Example.COM":       "example.com",
		"example.com.":      "example.com",
		"  example.com  ":   "example.com",
		"api-1.example.com": "api-1.example.com",
		"2001:db8::1":       "2001:db8::1",
		"":                  "",
		"exa mple.com":      "",
		"evil.com\x00ok":    "",
		"evil.com\nok":      "",
		"héllo.com":         "",
	}
	for in, want := range tests {
		if got := sanitizeHostname(in); got != want {
			t.Errorf("sanitizeHostname(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeHostname(strings.Repeat("a", 254)); got != "" {
		t.Errorf("over-long name = %q, want empty", got)
	}
}
