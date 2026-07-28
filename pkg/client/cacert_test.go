package client

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const samplePEM = `-----BEGIN CERTIFICATE-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA0Z3VS5JJcds3xfn/ygWy
-----END CERTIFICATE-----
`

func TestFormatAndResolveEscapedPEM(t *testing.T) {
	line := FormatCACertEnv([]byte(samplePEM))
	if !strings.HasPrefix(line, EnvCACert+`="`) {
		t.Fatalf("line=%q", line)
	}
	if strings.Contains(line, "\n") && !strings.Contains(line, `\n`) {
		t.Fatal("env line must not contain raw newlines")
	}
	// Extract quoted value
	val := strings.TrimPrefix(line, EnvCACert+`="`)
	val = strings.TrimSuffix(val, `"`)
	got, err := ResolveCACert(val)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != samplePEM {
		t.Fatalf("got=%q want=%q", got, samplePEM)
	}
}

func TestResolveCACertFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(path, []byte(samplePEM), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCACert(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != samplePEM {
		t.Fatalf("got=%q", got)
	}
}

func TestResolveCACertBase64(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte(samplePEM))
	got, err := ResolveCACert(b64)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != samplePEM {
		t.Fatalf("got=%q", got)
	}
}

func TestResolveCACertRawPEM(t *testing.T) {
	got, err := ResolveCACert(samplePEM)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != samplePEM {
		t.Fatalf("got=%q", got)
	}
}
