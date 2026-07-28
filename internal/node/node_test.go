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

func TestParseAvailability(t *testing.T) {
	a, err := node.ParseAvailability("")
	if err != nil || a != node.AvailabilityActive {
		t.Fatalf("empty: %v %v", a, err)
	}
	a, err = node.ParseAvailability("drain")
	if err != nil || a != node.AvailabilityDrain {
		t.Fatalf("drain: %v %v", a, err)
	}
	if _, err := node.ParseAvailability("bogus"); err == nil {
		t.Fatal("expected error")
	}
	if node.Availability("").Effective() != node.AvailabilityActive {
		t.Fatal("effective empty")
	}
	if node.AvailabilityPause.Schedulable() || node.AvailabilityDrain.Schedulable() {
		t.Fatal("pause/drain must not be schedulable")
	}
	if !node.AvailabilityActive.Schedulable() {
		t.Fatal("active must be schedulable")
	}
}
