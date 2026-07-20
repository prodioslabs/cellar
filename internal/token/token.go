package token

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/prodioslabs/cellar/internal/node"
)

const (
	prefix  = "CLLRN"
	version = "1"
)

// Secrets holds the role-specific join token secrets.
type Secrets struct {
	Worker  string `json:"worker"`
	Manager string `json:"manager"`
}

// GenerateSecrets creates new random secrets for worker and manager tokens.
func GenerateSecrets() (Secrets, error) {
	worker, err := randomSecret()
	if err != nil {
		return Secrets{}, err
	}
	manager, err := randomSecret()
	if err != nil {
		return Secrets{}, err
	}
	return Secrets{Worker: worker, Manager: manager}, nil
}

func randomSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Format builds a full join token for the given role.
// Format: CLLRN-1-<ca_digest_prefix>-<role_secret>
func Format(caDigestPrefix string, secrets Secrets, role node.Role) (string, error) {
	var secret string
	switch role {
	case node.RoleWorker:
		secret = secrets.Worker
	case node.RoleManager:
		secret = secrets.Manager
	default:
		return "", fmt.Errorf("invalid role %q", role)
	}
	return fmt.Sprintf("%s-%s-%s-%s", prefix, version, caDigestPrefix, secret), nil
}

// Pair holds both worker and manager join tokens.
type Pair struct {
	Worker  string `json:"worker"`
	Manager string `json:"manager"`
}

// FormatPair builds both role tokens.
func FormatPair(caDigestPrefix string, secrets Secrets) (Pair, error) {
	worker, err := Format(caDigestPrefix, secrets, node.RoleWorker)
	if err != nil {
		return Pair{}, err
	}
	manager, err := Format(caDigestPrefix, secrets, node.RoleManager)
	if err != nil {
		return Pair{}, err
	}
	return Pair{Worker: worker, Manager: manager}, nil
}

// Validate checks a join token against the CA digest and secrets.
// Returns the matched role on success.
func Validate(token string, caDigestPrefix string, secrets Secrets) (node.Role, error) {
	parts := strings.Split(token, "-")
	if len(parts) != 4 {
		return "", fmt.Errorf("invalid token format")
	}
	if parts[0] != prefix || parts[1] != version {
		return "", fmt.Errorf("invalid token prefix or version")
	}
	if parts[2] != caDigestPrefix {
		return "", fmt.Errorf("token CA digest mismatch")
	}
	secret := parts[3]
	switch secret {
	case secrets.Worker:
		return node.RoleWorker, nil
	case secrets.Manager:
		return node.RoleManager, nil
	default:
		return "", fmt.Errorf("invalid join token secret")
	}
}
