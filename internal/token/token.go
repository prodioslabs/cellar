package token

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	prefix  = "CLLRN"
	version = "1"
)

// GenerateSecret creates a new random join token secret.
func GenerateSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Format builds a full join token.
// Format: CLLRN-1-<ca_digest_prefix>-<secret>
func Format(caDigestPrefix, secret string) string {
	return fmt.Sprintf("%s-%s-%s-%s", prefix, version, caDigestPrefix, secret)
}

// Validate checks a join token against the CA digest and secret.
func Validate(token, caDigestPrefix, secret string) error {
	parts := strings.Split(token, "-")
	if len(parts) != 4 {
		return fmt.Errorf("invalid token format")
	}
	if parts[0] != prefix || parts[1] != version {
		return fmt.Errorf("invalid token prefix or version")
	}
	if parts[2] != caDigestPrefix {
		return fmt.Errorf("token CA digest mismatch")
	}
	if parts[3] != secret {
		return fmt.Errorf("invalid join token secret")
	}
	return nil
}
