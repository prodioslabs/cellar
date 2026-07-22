package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/prodioslabs/cellar/internal/node"
)

const (
	// DefaultCAValidity is how long the root CA certificate is valid.
	DefaultCAValidity = 10 * 365 * 24 * time.Hour
	// DefaultNodeValidity is how long issued node certificates are valid (90 days).
	DefaultNodeValidity = 2160 * time.Hour
)

// RootCA holds the cluster root CA certificate and private key.
type RootCA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
	KeyPEM  []byte
}

// GenerateRootCA creates a new ECDSA P-256 root CA.
func GenerateRootCA(org string, validity time.Duration) (*RootCA, error) {
	if validity <= 0 {
		validity = DefaultCAValidity
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{org},
			CommonName:   "cellar-ca",
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(validity),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return &RootCA{
		Cert:    cert,
		Key:     key,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	}, nil
}

// LoadRootCA loads a root CA from PEM-encoded certificate and key.
func LoadRootCA(certPEM, keyPEM []byte) (*RootCA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, errors.New("invalid CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, errors.New("invalid CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		// Try PKCS8 as a fallback.
		pk, err2 := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parse CA key: %w", err)
		}
		var ok bool
		key, ok = pk.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("CA key is not ECDSA")
		}
	}

	return &RootCA{
		Cert:    cert,
		Key:     key,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	}, nil
}

// DigestPrefix returns the first 25 hex characters of the SHA-256 of the CA DER,
// used in join tokens (Swarm-style).
func (r *RootCA) DigestPrefix() string {
	sum := sha256.Sum256(r.Cert.Raw)
	return hex.EncodeToString(sum[:])[:25]
}

// ParseCSR parses a PEM-encoded certificate signing request and validates it is ECDSA P-256.
func ParseCSR(csrPEM []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return nil, errors.New("invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature invalid: %w", err)
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("CSR public key must be ECDSA")
	}
	if pub.Curve != elliptic.P256() {
		return nil, errors.New("CSR public key must use P-256")
	}
	return csr, nil
}

// PublicKeyFingerprint returns a hex SHA-256 fingerprint of an ECDSA public key.
func PublicKeyFingerprint(pub *ecdsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

// IssueRequest holds parameters for signing a node certificate.
type IssueRequest struct {
	CSR      *x509.CertificateRequest
	NodeID   string
	Role     node.Role
	Validity time.Duration
}

// IssuedCert is the result of signing a CSR.
type IssuedCert struct {
	Cert    *x509.Certificate
	CertPEM []byte
}

// SignNodeCSR signs a CSR and issues a node identity certificate.
func (r *RootCA) SignNodeCSR(req IssueRequest) (*IssuedCert, error) {
	if req.CSR == nil {
		return nil, errors.New("CSR is required")
	}
	if req.NodeID == "" {
		return nil, errors.New("node ID is required")
	}
	if req.Role != node.RoleWorker && req.Role != node.RoleManager {
		return nil, fmt.Errorf("invalid role %q", req.Role)
	}
	validity := req.Validity
	if validity <= 0 {
		validity = DefaultNodeValidity
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:         req.NodeID,
			OrganizationalUnit: []string{req.Role.OU()},
			Organization:       []string{"cellar"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              []string{req.NodeID},
	}
	if req.Role == node.RoleManager {
		tmpl.DNSNames = append(tmpl.DNSNames, "cellar-manager", "cellar-ca")
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, r.Cert, req.CSR.PublicKey, r.Key)
	if err != nil {
		return nil, fmt.Errorf("sign certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse issued certificate: %w", err)
	}

	return &IssuedCert{
		Cert:    cert,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}, nil
}

// GenerateKeyAndCSR creates an ECDSA P-256 key and a CSR (optionally with CN set).
func GenerateKeyAndCSR(commonName string) (keyPEM []byte, csrPEM []byte, key *ecdsa.PrivateKey, err error) {
	key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate key: %w", err)
	}

	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: commonName},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create CSR: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal key: %w", err)
	}

	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	return keyPEM, csrPEM, key, nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	// Avoid serial 0.
	if serial.Sign() == 0 {
		serial = big.NewInt(1)
	}
	return serial, nil
}
