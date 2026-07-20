package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/prodioslabs/cellar/internal/api"
	"github.com/prodioslabs/cellar/internal/raftstore"
)

func main() {
	dataDir := flag.String("data-dir", "./cellar-data", "directory for CA and cluster state")
	listen := flag.String("listen", ":7946", "HTTP listen address")
	raftAddr := flag.String("raft-addr", "127.0.0.1:7947", "Raft TCP listen/advertise address (host:port)")
	nodeID := flag.String("node-id", "", "stable Raft server ID (default: basename of data-dir)")
	httpAdvertise := flag.String("http-advertise", "", "HTTP base URL advertised to peers (default: http://<listen>)")
	bootstrap := flag.Bool("bootstrap", false, "bootstrap a new single-manager Raft cluster")
	join := flag.String("join", "", "leader HTTP base URL to join as an additional manager")
	flag.Parse()

	id := *nodeID
	if id == "" {
		id = filepath.Base(*dataDir)
	}

	advertise := *httpAdvertise
	if advertise == "" {
		advertise = defaultHTTPAdvertise(*listen)
	}

	if *bootstrap && *join != "" {
		log.Fatal("cannot set both -bootstrap and -join")
	}

	rs, err := raftstore.Open(raftstore.Config{
		DataDir:       *dataDir,
		NodeID:        id,
		RaftAddr:      *raftAddr,
		HTTPAdvertise: advertise,
		Bootstrap:     *bootstrap,
	})
	if err != nil {
		log.Fatalf("raft: %v", err)
	}
	defer rs.Close()

	if *join != "" {
		if err := joinLeader(*join, raftstore.PeerInfo{
			NodeID:   id,
			RaftAddr: rs.RaftAddr(),
			HTTPAddr: advertise,
		}); err != nil {
			log.Fatalf("join: %v", err)
		}
		if err := rs.WaitForLeader(30 * time.Second); err != nil {
			log.Fatalf("wait leader: %v", err)
		}
	}

	srv := api.NewWithRaft(rs, rs)
	log.Printf("cellar manager listening on %s (raft=%s node-id=%s data-dir=%s bootstrap=%v)",
		*listen, rs.RaftAddr(), id, *dataDir, *bootstrap)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe(*listen)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("server: %v", err)
		}
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "received %s, shutting down\n", sig)
	}
}

func defaultHTTPAdvertise(listen string) string {
	host := listen
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	return "http://" + host
}

func joinLeader(leaderURL string, peer raftstore.PeerInfo) error {
	leaderURL = strings.TrimRight(leaderURL, "/")
	body, err := json.Marshal(map[string]string{
		"node_id":   peer.NodeID,
		"raft_addr": peer.RaftAddr,
		"http_addr": peer.HTTPAddr,
	})
	if err != nil {
		return err
	}

	var lastErr error
	for i := 0; i < 30; i++ {
		req, err := http.NewRequest(http.MethodPost, leaderURL+"/api/v1/cluster/managers", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		resp.Body.Close()
		if resp.StatusCode == http.StatusServiceUnavailable {
			if loc := resp.Header.Get("X-Cellar-Leader"); loc != "" {
				leaderURL = strings.TrimRight(loc, "/")
			}
			lastErr = fmt.Errorf("leader unavailable (status %d)", resp.StatusCode)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if errBody.Error != "" {
			return fmt.Errorf("join failed: %s", errBody.Error)
		}
		return fmt.Errorf("join failed: status %d", resp.StatusCode)
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("join failed after retries")
}
