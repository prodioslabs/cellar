package client

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// FormatCACertEnv returns a single .env line:
//
//	CELLAR_CA_CERT="-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n"
func FormatCACertEnv(pem []byte) string {
	escaped := escapePEMForEnv(string(pem))
	return EnvCACert + `="` + escaped + `"`
}

func escapePEMForEnv(pem string) string {
	// Normalize to \n escapes; also escape quotes for .env safety.
	s := strings.ReplaceAll(pem, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func unescapePEMFromEnv(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			case '"':
				b.WriteByte('"')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ResolveCACert turns a CELLAR_CA_CERT-style value into PEM bytes.
// Accepts: raw PEM, \n-escaped PEM, filesystem path, or std base64 of PEM.
func ResolveCACert(raw string) ([]byte, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("empty CA certificate value")
	}

	// Escaped or raw PEM (preserve trailing newline).
	unescaped := unescapePEMFromEnv(raw)
	if strings.Contains(unescaped, "BEGIN CERTIFICATE") {
		return []byte(unescaped), nil
	}

	raw = strings.TrimSpace(raw)

	// File path.
	if st, err := os.Stat(raw); err == nil && !st.IsDir() {
		b, err := os.ReadFile(raw)
		if err != nil {
			return nil, fmt.Errorf("read CA cert file: %w", err)
		}
		if !strings.Contains(string(b), "BEGIN CERTIFICATE") {
			return nil, fmt.Errorf("CA cert file does not contain a PEM certificate")
		}
		return b, nil
	}

	// Base64 of PEM.
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err == nil && strings.Contains(string(decoded), "BEGIN CERTIFICATE") {
		return decoded, nil
	}

	return nil, fmt.Errorf("CA certificate must be PEM, \\n-escaped PEM, a file path, or base64")
}
