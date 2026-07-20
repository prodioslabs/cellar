package api_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/api"
	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/store"
	"github.com/prodioslabs/cellar/pkg/agent"
)

func TestJoinRenewAndMTLS(t *testing.T) {
	dir := t.TempDir()
	fs, err := store.NewFileStore(filepath.Join(dir, "manager-data"))
	if err != nil {
		t.Fatal(err)
	}
	srv := api.New(fs)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	initBody := map[string]any{"cert_validity_hours": 24}
	payload, _ := json.Marshal(initBody)
	resp, err := http.Post(ts.URL+"/api/v1/cluster/init", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("init: %d %s", resp.StatusCode, b)
	}
	var initOut struct {
		ClusterID string `json:"cluster_id"`
		Tokens    struct {
			Worker  string `json:"worker"`
			Manager string `json:"manager"`
		} `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&initOut); err != nil {
		t.Fatal(err)
	}
	if initOut.Tokens.Worker == "" || initOut.Tokens.Manager == "" {
		t.Fatal("missing tokens")
	}

	resp2, err := http.Post(ts.URL+"/api/v1/cluster/init", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("expected conflict, got %d", resp2.StatusCode)
	}

	// Both roles use the same agent for join/renew/mTLS.
	workerAgent := agent.New(ts.URL, filepath.Join(dir, "worker"))
	workerID, err := workerAgent.Join(t.Context(), initOut.Tokens.Worker)
	if err != nil {
		t.Fatal(err)
	}
	if workerID.Role != node.RoleWorker {
		t.Fatalf("role=%s", workerID.Role)
	}
	if workerAgent.CanAccessControlPlane() {
		t.Fatal("worker must not access control plane")
	}

	managerAgent := agent.New(ts.URL, filepath.Join(dir, "manager-node"))
	managerID, err := managerAgent.Join(t.Context(), initOut.Tokens.Manager)
	if err != nil {
		t.Fatal(err)
	}
	if managerID.Role != node.RoleManager {
		t.Fatalf("role=%s", managerID.Role)
	}
	if !managerAgent.CanAccessControlPlane() {
		t.Fatal("manager must access control plane")
	}

	st, err := http.Get(ts.URL + "/api/v1/ca/status/" + workerID.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Body.Close()
	if st.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", st.StatusCode)
	}

	renewed, err := workerAgent.Renew(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if renewed.NodeID != workerID.NodeID {
		t.Fatalf("node id changed on renew: %s -> %s", workerID.NodeID, renewed.NodeID)
	}

	workerTLS, err := workerAgent.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	managerTLS, err := managerAgent.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}

	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "no peer cert", 500)
			return
		}
		_, _ = io.WriteString(w, r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	peer.TLS = managerTLS
	peer.StartTLS()
	defer peer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates:       workerTLS.Certificates,
				RootCAs:            workerTLS.RootCAs,
				InsecureSkipVerify: true,
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					if len(rawCerts) == 0 {
						return io.ErrUnexpectedEOF
					}
					cert, err := x509.ParseCertificate(rawCerts[0])
					if err != nil {
						return err
					}
					opts := x509.VerifyOptions{Roots: workerTLS.RootCAs, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
					_, err = cert.Verify(opts)
					return err
				},
			},
		},
	}
	r, err := client.Get(peer.URL)
	if err != nil {
		t.Fatalf("mTLS get: %v", err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if string(body) != workerID.NodeID {
		t.Fatalf("peer saw CN %q, want %q", body, workerID.NodeID)
	}

	// Control-plane rotate over plain HTTP (bootstrap) still works.
	rot, err := http.Post(ts.URL+"/api/v1/cluster/rotate-tokens", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rot.Body.Close()
	if rot.StatusCode != http.StatusOK {
		t.Fatalf("rotate: %d", rot.StatusCode)
	}

	_, csrPEM, _, err := ca.GenerateKeyAndCSR("")
	if err != nil {
		t.Fatal(err)
	}
	badJoin, _ := json.Marshal(map[string]string{"csr": string(csrPEM), "token": initOut.Tokens.Worker})
	badResp, err := http.Post(ts.URL+"/api/v1/ca/issue", "application/json", bytes.NewReader(badJoin))
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized after rotate, got %d", badResp.StatusCode)
	}

	for _, name := range []string{"ca.crt", "ca.key"} {
		if _, err := os.Stat(filepath.Join(dir, "manager-data", "ca", name)); err != nil {
			t.Fatal(err)
		}
	}
	block, _ := pem.Decode([]byte(workerID.Certificate))
	if block == nil {
		t.Fatal("worker cert not PEM")
	}
}

func TestControlPlaneRejectsWorkerClientCert(t *testing.T) {
	dir := t.TempDir()
	fs, err := store.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := api.New(fs)

	// Bootstrap over plain HTTP first.
	plain := httptest.NewServer(srv.Handler())
	defer plain.Close()

	resp, err := http.Post(plain.URL+"/api/v1/cluster/init", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	var initOut struct {
		Tokens struct {
			Worker  string `json:"worker"`
			Manager string `json:"manager"`
		} `json:"tokens"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&initOut)
	resp.Body.Close()

	worker := agent.New(plain.URL, filepath.Join(dir, "worker"))
	if _, err := worker.Join(t.Context(), initOut.Tokens.Worker); err != nil {
		t.Fatal(err)
	}
	manager := agent.New(plain.URL, filepath.Join(dir, "manager"))
	if _, err := manager.Join(t.Context(), initOut.Tokens.Manager); err != nil {
		t.Fatal(err)
	}

	root, err := fs.GetRootCA(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := manager.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	// Server presents manager cert; requires client certs signed by cluster CA.
	serverTLS.ClientAuth = tls.RequireAndVerifyClientCert
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(root.CertPEM)
	serverTLS.ClientCAs = pool

	tlsSrv := httptest.NewUnstartedServer(srv.Handler())
	tlsSrv.TLS = serverTLS
	tlsSrv.StartTLS()
	defer tlsSrv.Close()

	workerTLS, err := worker.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	managerTLS, err := manager.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}

	workerClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates:       workerTLS.Certificates,
			RootCAs:            pool,
			InsecureSkipVerify: true,
		},
	}}
	managerClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates:       managerTLS.Certificates,
			RootCAs:            pool,
			InsecureSkipVerify: true,
		},
	}}

	workerResp, err := workerClient.Get(tlsSrv.URL + "/api/v1/cluster/tokens")
	if err != nil {
		t.Fatal(err)
	}
	workerResp.Body.Close()
	if workerResp.StatusCode != http.StatusForbidden {
		t.Fatalf("worker expected 403, got %d", workerResp.StatusCode)
	}

	managerResp, err := managerClient.Get(tlsSrv.URL + "/api/v1/cluster/tokens")
	if err != nil {
		t.Fatal(err)
	}
	managerResp.Body.Close()
	if managerResp.StatusCode != http.StatusOK {
		t.Fatalf("manager expected 200, got %d", managerResp.StatusCode)
	}
}

func TestIssueRequiresTokenOrNodeID(t *testing.T) {
	dir := t.TempDir()
	fs, err := store.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := api.New(fs)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/cluster/init", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	_, csrPEM, _, err := ca.GenerateKeyAndCSR("")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"csr": string(csrPEM)})
	r, err := http.Post(ts.URL+"/api/v1/ca/issue", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d", r.StatusCode)
	}
}

func TestCertOU(t *testing.T) {
	dir := t.TempDir()
	fs, err := store.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := api.New(fs)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/cluster/init", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	var initOut struct {
		Tokens struct {
			Manager string `json:"manager"`
		} `json:"tokens"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&initOut)
	resp.Body.Close()

	a := agent.New(ts.URL, filepath.Join(dir, "n"))
	id, err := a.Join(t.Context(), initOut.Tokens.Manager)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(id.Certificate))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.NotAfter.Sub(cert.NotBefore) < 80*24*time.Hour {
		t.Logf("validity window: %v", cert.NotAfter.Sub(cert.NotBefore))
	}
	role, err := node.ParseOU(cert.Subject.OrganizationalUnit[0])
	if err != nil || role != node.RoleManager {
		t.Fatalf("OU role=%v err=%v", role, err)
	}
}
