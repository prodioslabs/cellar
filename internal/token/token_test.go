package token_test

import (
	"strings"
	"testing"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/token"
)

func TestFormatAndValidate(t *testing.T) {
	secrets, err := token.GenerateSecrets()
	if err != nil {
		t.Fatal(err)
	}
	digest := "abcdefghijklmnopqrstuvwxy" // 25 chars

	worker, err := token.Format(digest, secrets, node.RoleWorker)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := token.Format(digest, secrets, node.RoleManager)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(worker, "CLLRN-1-"+digest+"-") {
		t.Fatalf("unexpected worker token: %s", worker)
	}
	if worker == manager {
		t.Fatal("worker and manager tokens must differ")
	}

	role, err := token.Validate(worker, digest, secrets)
	if err != nil || role != node.RoleWorker {
		t.Fatalf("worker validate: role=%s err=%v", role, err)
	}
	role, err = token.Validate(manager, digest, secrets)
	if err != nil || role != node.RoleManager {
		t.Fatalf("manager validate: role=%s err=%v", role, err)
	}
}

func TestValidateRejectsBadDigest(t *testing.T) {
	secrets := token.Secrets{Worker: "aaa", Manager: "bbb"}
	tok, err := token.Format("abcdefghijklmnopqrstuvwxy", secrets, node.RoleWorker)
	if err != nil {
		t.Fatal(err)
	}
	_, err = token.Validate(tok, "zzzzzzzzzzzzzzzzzzzzzzzzz", secrets)
	if err == nil {
		t.Fatal("expected digest mismatch error")
	}
}

func TestValidateRejectsBadSecret(t *testing.T) {
	secrets := token.Secrets{Worker: "aaa", Manager: "bbb"}
	_, err := token.Validate("CLLRN-1-abcdefghijklmnopqrstuvwxy-badsecret", "abcdefghijklmnopqrstuvwxy", secrets)
	if err == nil {
		t.Fatal("expected invalid secret error")
	}
}
