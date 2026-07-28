package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	Prefix    = "cellar_"
	keyBytes  = 20 // 40 hex chars
	maskHead  = 2
	maskTail  = 4
)

// Key is the Raft-replicated API key record (secret never stored).
type Key struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	KeyHash   string    `json:"key_hash"`
	Mask      string    `json:"mask"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
	Disabled  bool      `json:"disabled,omitempty"`
}

// Clone returns a deep copy.
func Clone(k *Key) *Key {
	if k == nil {
		return nil
	}
	cp := *k
	return &cp
}

// Generated is a newly minted key; Raw is shown once to the caller.
type Generated struct {
	Key *Key
	Raw string
}

// Generate creates a new API key with a random secret.
func Generate(name string) (*Generated, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	secret := make([]byte, keyBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	hexSecret := hex.EncodeToString(secret)
	raw := Prefix + hexSecret
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	k := &Key{
		ID:        id,
		Name:      name,
		KeyHash:   Hash(raw),
		Mask:      Mask(raw),
		CreatedAt: now,
	}
	return &Generated{Key: k, Raw: raw}, nil
}

// Hash returns the hex-encoded SHA-256 of the raw API key string.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Mask returns a display form like cellar_ab…wxyz.
func Mask(raw string) string {
	if !strings.HasPrefix(raw, Prefix) {
		return Prefix + "****"
	}
	body := strings.TrimPrefix(raw, Prefix)
	if len(body) < maskHead+maskTail {
		return Prefix + "****"
	}
	return Prefix + body[:maskHead] + "…" + body[len(body)-maskTail:]
}

// EqualHash compares two hash strings in constant time.
func EqualHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ParseRaw validates the cellar_ prefix and length; returns the raw key unchanged.
func ParseRaw(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, Prefix) {
		return "", fmt.Errorf("invalid API key prefix")
	}
	body := strings.TrimPrefix(raw, Prefix)
	if len(body) != keyBytes*2 {
		return "", fmt.Errorf("invalid API key length")
	}
	if _, err := hex.DecodeString(body); err != nil {
		return "", fmt.Errorf("invalid API key encoding")
	}
	return raw, nil
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
