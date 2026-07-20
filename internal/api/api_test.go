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
	fs, err := store.NewFileStore(filepath.Join(dir, "manager"))
	if err != nil {
		t.Fatal(err)
	}
	srv := api.New(fs)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Init cluster with short validity so renewal logic is easy to reason about.
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

	// Double init should conflict.
	resp2, err := http.Post(ts.URL+"/api/v1/cluster/init", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("expected conflict, got %d", resp2.StatusCode)
	}

	workerAgent := agent.New(ts.URL, filepath.Join(dir, "worker"))
	workerID, err := workerAgent.Join(t.Context(), initOut.Tokens.Worker)
	if err != nil {
		t.Fatal(err)
	}
	if workerID.Role != node.RoleWorker {
		t.Fatalf("role=%s", workerID.Role)
	}

	managerAgent := agent.New(ts.URL, filepath.Join(dir, "manager-node"))
	managerID, err := managerAgent.Join(t.Context(), initOut.Tokens.Manager)
	if err != nil {
		t.Fatal(err)
	}
	if managerID.Role != node.RoleManager {
		t.Fatalf("role=%s", managerID.Role)
	}

	// Status endpoint.
	st, err := http.Get(ts.URL + "/api/v1/ca/status/" + workerID.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Body.Close()
	if st.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", st.StatusCode)
	}

	// Renew worker.
	renewed, err := workerAgent.Renew(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if renewed.NodeID != workerID.NodeID {
		t.Fatalf("node id changed on renew: %s -> %s", workerID.NodeID, renewed.NodeID)
	}

	// mTLS between worker and manager using issued certs.
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
				Certificates: workerTLS.Certificates,
				RootCAs:      workerTLS.RootCAs,
				// httptest uses 127.0.0.1; our certs have no SAN — skip verify hostname only.
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

	// Rotate tokens and ensure old token fails.
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

	// Ensure PEM files exist on disk.
	for _, name := range []string{"ca.crt", "ca.key"} {
		if _, err := os.Stat(filepath.Join(dir, "manager", "ca", name)); err != nil {
			t.Fatal(err)
		}
	}
	block, _ := pem.Decode([]byte(workerID.Certificate))
	if block == nil {
		t.Fatal("worker cert not PEM")
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
	json.NewDecoder(resp.Body).Decode(&initOut)
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
		// default 90d validity with small skew
		t.Logf("validity window: %v", cert.NotAfter.Sub(cert.NotBefore))
	}
	role, err := node.ParseOU(cert.Subject.OrganizationalUnit[0])
	if err != nil || role != node.RoleManager {
		t.Fatalf("OU role=%v err=%v", role, err)
	}
}
