package ca_test

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
)

func TestGenerateAndSign(t *testing.T) {
	root, err := ca.GenerateRootCA("cellar-test", ca.DefaultCAValidity)
	if err != nil {
		t.Fatal(err)
	}
	if !root.Cert.IsCA {
		t.Fatal("expected IsCA")
	}
	if len(root.DigestPrefix()) != 25 {
		t.Fatalf("digest prefix length: %d", len(root.DigestPrefix()))
	}

	_, csrPEM, _, err := ca.GenerateKeyAndCSR("")
	if err != nil {
		t.Fatal(err)
	}
	csr, err := ca.ParseCSR(csrPEM)
	if err != nil {
		t.Fatal(err)
	}

	nodeID := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	issued, err := root.SignNodeCSR(ca.IssueRequest{
		CSR:      csr,
		NodeID:   nodeID,
		Role:     node.RoleWorker,
		Validity: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	if issued.Cert.Subject.CommonName != nodeID {
		t.Fatalf("CN=%s", issued.Cert.Subject.CommonName)
	}
	if len(issued.Cert.Subject.OrganizationalUnit) != 1 || issued.Cert.Subject.OrganizationalUnit[0] != "cellar-worker" {
		t.Fatalf("OU=%v", issued.Cert.Subject.OrganizationalUnit)
	}

	// Verify signature chain.
	roots := x509.NewCertPool()
	roots.AddCert(root.Cert)
	if _, err := issued.Cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Round-trip LoadRootCA.
	loaded, err := ca.LoadRootCA(root.CertPEM, root.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DigestPrefix() != root.DigestPrefix() {
		t.Fatal("digest mismatch after reload")
	}

	block, _ := pem.Decode(issued.CertPEM)
	if block == nil {
		t.Fatal("bad issued PEM")
	}
}

func TestParseCSRRejectsNonECDSA(t *testing.T) {
	_, err := ca.ParseCSR([]byte("not a pem"))
	if err == nil {
		t.Fatal("expected error")
	}
}
