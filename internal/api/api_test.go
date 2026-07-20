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
	fs, err := store.NewFileStore(filepath.Join(dir, "ca-server"))
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
		Token     string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&initOut); err != nil {
		t.Fatal(err)
	}
	if initOut.Token == "" {
		t.Fatal("missing join token")
	}

	resp2, err := http.Post(ts.URL+"/api/v1/cluster/init", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("expected conflict, got %d", resp2.StatusCode)
	}

	nodeA := agent.New(ts.URL, filepath.Join(dir, "node-a"))
	idA, err := nodeA.Join(t.Context(), initOut.Token)
	if err != nil {
		t.Fatal(err)
	}
	if idA.Role != node.RoleCellarNode {
		t.Fatalf("role=%s", idA.Role)
	}

	nodeB := agent.New(ts.URL, filepath.Join(dir, "node-b"))
	idB, err := nodeB.Join(t.Context(), initOut.Token)
	if err != nil {
		t.Fatal(err)
	}
	if idB.Role != node.RoleCellarNode {
		t.Fatalf("role=%s", idB.Role)
	}

	st, err := http.Get(ts.URL + "/api/v1/ca/status/" + idA.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Body.Close()
	if st.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", st.StatusCode)
	}

	renewed, err := nodeA.Renew(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if renewed.NodeID != idA.NodeID {
		t.Fatalf("node id changed on renew: %s -> %s", idA.NodeID, renewed.NodeID)
	}

	tlsA, err := nodeA.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	tlsB, err := nodeB.TLSConfig()
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
	peer.TLS = tlsB
	peer.StartTLS()
	defer peer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates:       tlsA.Certificates,
				RootCAs:            tlsA.RootCAs,
				InsecureSkipVerify: true,
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					if len(rawCerts) == 0 {
						return io.ErrUnexpectedEOF
					}
					cert, err := x509.ParseCertificate(rawCerts[0])
					if err != nil {
						return err
					}
					opts := x509.VerifyOptions{Roots: tlsA.RootCAs, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	if string(body) != idA.NodeID {
		t.Fatalf("peer saw CN %q, want %q", body, idA.NodeID)
	}

	rot, err := http.Post(ts.URL+"/api/v1/cluster/rotate-token", "application/json", nil)
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
	badJoin, _ := json.Marshal(map[string]string{"csr": string(csrPEM), "token": initOut.Token})
	badResp, err := http.Post(ts.URL+"/api/v1/ca/issue", "application/json", bytes.NewReader(badJoin))
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized after rotate, got %d", badResp.StatusCode)
	}

	for _, name := range []string{"ca.crt", "ca.key"} {
		if _, err := os.Stat(filepath.Join(dir, "ca-server", "ca", name)); err != nil {
			t.Fatal(err)
		}
	}
	block, _ := pem.Decode([]byte(idA.Certificate))
	if block == nil {
		t.Fatal("node cert not PEM")
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
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&initOut)
	resp.Body.Close()

	a := agent.New(ts.URL, filepath.Join(dir, "n"))
	id, err := a.Join(t.Context(), initOut.Token)
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
	if err != nil || role != node.RoleCellarNode {
		t.Fatalf("OU role=%v err=%v", role, err)
	}
}
