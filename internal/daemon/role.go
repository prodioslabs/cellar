package daemon

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/grpcapi"
	"github.com/prodioslabs/cellar/internal/identity"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/raftstore"
)

// applyRoleChange transitions this daemon to desired after the leader has
// already updated the Raft node record. It re-issues the leaf cert (OU/SANs)
// and opens or closes Raft accordingly.
func (d *Daemon) applyRoleChange(ctx context.Context, desired node.Role) error {
	mat := d.idStore.Material()
	if mat == nil {
		return fmt.Errorf("no identity")
	}
	if mat.Role == desired {
		return nil
	}

	switch desired {
	case node.RoleManager:
		return d.promoteLocal(ctx)
	case node.RoleWorker:
		return d.demoteLocal(ctx)
	default:
		return fmt.Errorf("unknown role %q", desired)
	}
}

func (d *Daemon) promoteLocal(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	mat := d.idStore.Material()
	if mat == nil {
		return fmt.Errorf("no identity")
	}
	if mat.Role == node.RoleManager && d.raft != nil {
		return nil
	}
	state := d.idStore.State()
	mgrAddr := d.managerDialAddrLocked()
	if mgrAddr == "" {
		return fmt.Errorf("no manager address for promote")
	}

	newMat, err := d.forceRenewLocked(ctx, mgrAddr, mat)
	if err != nil {
		return fmt.Errorf("reissue manager cert: %w", err)
	}
	if node.Role(newMat.Role) != node.RoleManager {
		return fmt.Errorf("expected manager cert after promote, got %s", newMat.Role)
	}

	raftAddr := state.RaftAddr
	if raftAddr == "" {
		raftAddr = d.cfg.RaftAddr
	}
	raftAddr = defaultRaftAddr(raftAddr)
	advertise := state.AdvertiseAddr
	if advertise == "" {
		advertise = defaultAdvertise(state.ListenAddr)
	}
	listen := state.ListenAddr
	if listen == "" {
		listen = d.cfg.ListenAddr
	}

	state.Role = node.RoleManager
	state.RaftAddr = raftAddr
	if err := d.idStore.Save(newMat, state); err != nil {
		return err
	}

	rs, err := raftstore.Open(raftstore.Config{
		DataDir:       d.cfg.DataDir,
		NodeID:        newMat.NodeID,
		RaftAddr:      raftAddr,
		GRPCAdvertise: advertise,
		Bootstrap:     false,
	})
	if err != nil {
		return fmt.Errorf("open raft: %w", err)
	}
	if err := grpcapi.RaftJoin(ctx, mgrAddr, newMat.Certificate, newMat.PrivateKey, newMat.CACert, newMat.NodeID, rs.RaftAddr(), advertise); err != nil {
		_ = rs.Close()
		return fmt.Errorf("raft join: %w", err)
	}
	if err := rs.WaitForLeader(30 * time.Second); err != nil {
		_ = rs.Close()
		return err
	}
	if err := rs.WaitInitialized(30 * time.Second); err != nil {
		_ = rs.Close()
		return err
	}
	cluster, err := rs.GetCluster(ctx)
	if err != nil {
		_ = rs.Close()
		return err
	}
	newMat.ClusterID = cluster.ClusterID
	state.ClusterID = cluster.ClusterID
	if err := d.idStore.Save(newMat, state); err != nil {
		_ = rs.Close()
		return err
	}

	d.raft = rs
	d.caServer = grpcapi.NewCAServer(rs, rs)
	d.sandboxServer = grpcapi.NewSandboxServer(rs, rs)
	d.sandboxAPI = grpcapi.NewSandboxAPIServer(rs, rs, d.sandboxServer, d)
	_ = d.caServer.UpdateRootCA(ctx)

	clusterCtx := d.ensureClusterCtxLocked()
	d.clusterWG.Add(1)
	go func() {
		defer d.clusterWG.Done()
		d.watchLeadership(clusterCtx)
	}()

	if err := d.restartRemoteGRPCLocked(listen); err != nil {
		return err
	}
	log.Printf("promoted to manager (%s)", shortID(newMat.NodeID))
	return nil
}

func (d *Daemon) demoteLocal(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	mat := d.idStore.Material()
	if mat == nil {
		return fmt.Errorf("no identity")
	}
	if mat.Role == node.RoleWorker && d.raft == nil {
		return nil
	}
	state := d.idStore.State()
	mgrAddr := d.managerDialAddrLocked()
	if mgrAddr == "" {
		mgrAddr = state.ManagerAddr
	}
	if mgrAddr == "" {
		mgrAddr = state.AdvertiseAddr
	}
	if mgrAddr == "" {
		return fmt.Errorf("no manager address for demote renew")
	}

	raft := d.raft
	d.raft = nil
	d.caServer = nil
	d.sandboxServer = nil
	d.sandboxAPI = nil
	if raft != nil {
		_ = raft.Close()
	}
	raftDir := filepath.Join(d.cfg.DataDir, "raft")
	_ = os.RemoveAll(raftDir)

	state.Role = node.RoleWorker
	state.ManagerAddr = mgrAddr
	mat.Role = node.RoleWorker
	if err := d.idStore.Save(mat, state); err != nil {
		return err
	}

	newMat, err := d.forceRenewLocked(ctx, mgrAddr, mat)
	if err != nil {
		return fmt.Errorf("reissue worker cert: %w", err)
	}
	newMat.Role = node.RoleWorker
	if err := d.idStore.Save(newMat, state); err != nil {
		return err
	}

	listen := state.ListenAddr
	if listen == "" {
		listen = d.cfg.ListenAddr
	}
	if err := d.restartRemoteGRPCLocked(listen); err != nil {
		return err
	}
	log.Printf("demoted to worker (%s)", shortID(newMat.NodeID))
	return nil
}

func (d *Daemon) forceRenewLocked(ctx context.Context, managerAddr string, mat *identity.Material) (*identity.Material, error) {
	keyPEM, csrPEM, _, err := ca.GenerateKeyAndCSR(mat.NodeID)
	if err != nil {
		return nil, err
	}
	issued, err := grpcapi.IssueRenew(ctx, managerAddr, mat.Certificate, mat.PrivateKey, mat.CACert, mat.NodeID, csrPEM)
	if err != nil {
		return nil, err
	}
	newMat := &identity.Material{
		NodeID:      issued.NodeId,
		Role:        node.Role(issued.Role),
		ClusterID:   mat.ClusterID,
		Certificate: issued.Certificate,
		PrivateKey:  keyPEM,
		CACert:      mat.CACert,
		NotAfter:    time.Unix(0, issued.ExpiresAtUnixNano).UTC(),
	}
	if block, _ := pem.Decode(issued.Certificate); block != nil {
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			newMat.NotBefore = cert.NotBefore
			newMat.NotAfter = cert.NotAfter
		}
	}
	return newMat, nil
}

func (d *Daemon) managerDialAddrLocked() string {
	if d.raft != nil {
		if addr := d.raft.LeaderGRPC(); addr != "" {
			return addr
		}
		if addr := d.raft.GRPCAdvertise(); addr != "" {
			return addr
		}
	}
	state := d.idStore.State()
	if state.ManagerAddr != "" {
		return state.ManagerAddr
	}
	return state.AdvertiseAddr
}

func (d *Daemon) restartRemoteGRPCLocked(listenAddr string) error {
	remoteGRPC := d.remoteGRPC
	remoteLis := d.remoteLis
	d.remoteGRPC = nil
	d.remoteLis = nil
	if remoteGRPC != nil {
		gracefulStop(remoteGRPC, gracefulStopTimeout)
	}
	if remoteLis != nil {
		_ = remoteLis.Close()
	}
	return d.startRemoteGRPCLocked(listenAddr)
}

// maybeApplyDesiredRole reacts to store role / removal signaled via heartbeat.
func (d *Daemon) maybeApplyDesiredRole(ctx context.Context, desiredRole string, removed bool) {
	if removed {
		log.Printf("node record removed from cluster; clearing local identity (run cellar leave or re-join)")
		d.stopClusterLocal()
		if err := d.idStore.Clear(); err != nil {
			log.Printf("clear identity after removal: %v", err)
		}
		_ = os.RemoveAll(filepath.Join(d.cfg.DataDir, "raft"))
		return
	}
	if desiredRole == "" {
		return
	}
	desired := node.Role(desiredRole)
	mat := d.idStore.Material()
	if mat == nil || mat.Role == desired {
		return
	}
	if err := d.applyRoleChange(ctx, desired); err != nil {
		log.Printf("apply role change to %s: %v", desired, err)
	}
}
