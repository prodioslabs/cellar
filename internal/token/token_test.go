package token_test

import (
	"strings"
	"testing"

	"github.com/prodioslabs/cellar/internal/token"
)

func TestFormatAndValidate(t *testing.T) {
	secret, err := token.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	digest := "abcdefghijklmnopqrstuvwxy" // 25 chars

	tok := token.Format(digest, secret)
	if !strings.HasPrefix(tok, "CLLRN-1-"+digest+"-") {
		t.Fatalf("unexpected token: %s", tok)
	}
	if err := token.Validate(tok, digest, secret); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsBadDigest(t *testing.T) {
	secret := "aaa"
	tok := token.Format("abcdefghijklmnopqrstuvwxy", secret)
	if err := token.Validate(tok, "zzzzzzzzzzzzzzzzzzzzzzzzz", secret); err == nil {
		t.Fatal("expected digest mismatch error")
	}
}

func TestValidateRejectsBadSecret(t *testing.T) {
	err := token.Validate("CLLRN-1-abcdefghijklmnopqrstuvwxy-badsecret", "abcdefghijklmnopqrstuvwxy", "aaa")
	if err == nil {
		t.Fatal("expected invalid secret error")
	}
}
