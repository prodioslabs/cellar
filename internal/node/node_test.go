package node_test

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"

	"github.com/prodioslabs/cellar/internal/node"
)

func TestControlPlaneAccess(t *testing.T) {
	if !node.RoleManager.CanAccessControlPlane() {
		t.Fatal("manager should access control plane")
	}
	if node.RoleWorker.CanAccessControlPlane() {
		t.Fatal("worker should not access control plane")
	}
}

func TestRoleFromCertificate(t *testing.T) {
	cert := &x509.Certificate{
		Subject: pkix.Name{OrganizationalUnit: []string{node.RoleManager.OU()}},
	}
	role, err := node.RoleFromCertificate(cert)
	if err != nil || role != node.RoleManager {
		t.Fatalf("role=%s err=%v", role, err)
	}

	cert.Subject.OrganizationalUnit = []string{node.RoleWorker.OU()}
	role, err = node.RoleFromCertificate(cert)
	if err != nil || role != node.RoleWorker {
		t.Fatalf("role=%s err=%v", role, err)
	}
}
