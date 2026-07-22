package grpcapi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/token"
)

const TLSServerName = "cellar-manager"

// DownloadRootCA fetches the public CA cert and verifies it against the join token digest.
func DownloadRootCA(ctx context.Context, remoteAddr, joinToken string) ([]byte, error) {
	parsed, err := token.Parse(joinToken)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(
		normalizeAddr(remoteAddr),
		grpc.WithTransportCredentials(credentials.NewTLS(InsecureTLSConfig())),
	)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	resp, err := cellarv1.NewCAClient(conn).GetRootCACertificate(ctx, &cellarv1.GetRootCACertificateRequest{})
	if err != nil {
		return nil, err
	}
	if err := verifyCADigest(resp.Certificate, parsed.CADigestPrefix); err != nil {
		return nil, err
	}
	return resp.Certificate, nil
}

func verifyCADigest(certPEM []byte, wantPrefix string) error {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("invalid CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(cert.Raw)
	got := hex.EncodeToString(sum[:])[:25]
	if got != wantPrefix {
		return fmt.Errorf("CA digest mismatch: token pins %s, got %s", wantPrefix, got)
	}
	return nil
}

// IssueWithToken requests a leaf certificate using a join token.
func IssueWithToken(ctx context.Context, remoteAddr string, caPEM []byte, joinToken string, csrPEM []byte) (*cellarv1.IssueNodeCertificateResponse, error) {
	tlsCfg, err := ClientTLSFromPEMs(nil, nil, caPEM, TLSServerName)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(normalizeAddr(remoteAddr), grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return cellarv1.NewNodeCAClient(conn).IssueNodeCertificate(ctx, &cellarv1.IssueNodeCertificateRequest{
		Csr:   csrPEM,
		Token: joinToken,
	})
}

// IssueRenew renews a certificate using mTLS.
func IssueRenew(ctx context.Context, remoteAddr string, certPEM, keyPEM, caPEM []byte, nodeID string, csrPEM []byte) (*cellarv1.IssueNodeCertificateResponse, error) {
	tlsCfg, err := ClientTLSFromPEMs(certPEM, keyPEM, caPEM, TLSServerName)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(normalizeAddr(remoteAddr), grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return cellarv1.NewNodeCAClient(conn).IssueNodeCertificate(ctx, &cellarv1.IssueNodeCertificateRequest{
		Csr:    csrPEM,
		NodeId: nodeID,
	})
}

// RaftJoin asks the leader to add this manager as a voter (mTLS required).
func RaftJoin(ctx context.Context, remoteAddr string, certPEM, keyPEM, caPEM []byte, nodeID, raftAddr, grpcAddr string) error {
	tlsCfg, err := ClientTLSFromPEMs(certPEM, keyPEM, caPEM, TLSServerName)
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(normalizeAddr(remoteAddr), grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = cellarv1.NewRaftMembershipClient(conn).Join(ctx, &cellarv1.RaftJoinRequest{
		NodeId:   nodeID,
		RaftAddr: raftAddr,
		GrpcAddr: grpcAddr,
	})
	return err
}

func normalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(addr, "17946")
}

// SelfIssue creates a local manager leaf using an in-memory root (init path).
func SelfIssue(root *ca.RootCA, role node.Role, validity time.Duration) (nodeID string, certPEM, keyPEM []byte, notBefore, notAfter time.Time, err error) {
	nodeID, err = node.NewID()
	if err != nil {
		return "", nil, nil, time.Time{}, time.Time{}, err
	}
	keyPEM, csrPEM, _, err := ca.GenerateKeyAndCSR(nodeID)
	if err != nil {
		return "", nil, nil, time.Time{}, time.Time{}, err
	}
	csr, err := ca.ParseCSR(csrPEM)
	if err != nil {
		return "", nil, nil, time.Time{}, time.Time{}, err
	}
	issued, err := root.SignNodeCSR(ca.IssueRequest{
		CSR:      csr,
		NodeID:   nodeID,
		Role:     role,
		Validity: validity,
	})
	if err != nil {
		return "", nil, nil, time.Time{}, time.Time{}, err
	}
	return nodeID, issued.CertPEM, keyPEM, issued.Cert.NotBefore, issued.Cert.NotAfter, nil
}

// DialInsecure is used only when no better option exists (tests).
func DialInsecure(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(normalizeAddr(addr), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		InsecureSkipVerify: true, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
	})))
}
