package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/blob"
	"github.com/prodioslabs/cellar/internal/grpcapi"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

const snapshotReaperTick = 5 * time.Minute

func (d *Daemon) blobConfig() blob.Config {
	cfg := blob.Config{
		DataDir:     d.cfg.DataDir,
		SnapshotDir: firstNonEmpty(d.cfg.SnapshotDir, os.Getenv("CELLAR_SNAPSHOT_DIR")),
		Bucket:      firstNonEmpty(d.cfg.S3Bucket, os.Getenv("CELLAR_S3_BUCKET")),
		Region:      firstNonEmpty(d.cfg.S3Region, os.Getenv("CELLAR_S3_REGION")),
		Prefix:      firstNonEmpty(d.cfg.S3Prefix, os.Getenv("CELLAR_S3_PREFIX")),
		Endpoint:    firstNonEmpty(d.cfg.S3Endpoint, os.Getenv("CELLAR_S3_ENDPOINT")),
	}
	return cfg
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (d *Daemon) ensureBlobStore() (blob.Store, error) {
	d.mu.Lock()
	if d.blobs != nil {
		b := d.blobs
		d.mu.Unlock()
		return b, nil
	}
	d.mu.Unlock()
	store, err := blob.Open(context.Background(), d.blobConfig())
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.blobs == nil {
		d.blobs = store
	} else {
		store = d.blobs
	}
	d.mu.Unlock()
	return store, nil
}

func (d *Daemon) newSandboxServer(rs *raftstore.Store) *grpcapi.SandboxServer {
	sb := grpcapi.NewSandboxServer(rs, rs, d)
	backend := string(sandbox.FSSnapshotBackendLocal)
	if firstNonEmpty(d.cfg.S3Bucket, os.Getenv("CELLAR_S3_BUCKET")) != "" {
		backend = string(sandbox.FSSnapshotBackendS3)
	}
	sb.SetSnapshotRuntime(d, backend)
	return sb
}

func (d *Daemon) snapshotReaperLoop(ctx context.Context) {
	ticker := time.NewTicker(snapshotReaperTick)
	defer ticker.Stop()
	d.runSnapshotReaper(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.runSnapshotReaper(ctx)
		}
	}
}

func (d *Daemon) runSnapshotReaper(ctx context.Context) {
	d.mu.Lock()
	raft := d.raft
	sbSrv := d.sandboxServer
	d.mu.Unlock()
	if raft == nil || !raft.IsLeader() || sbSrv == nil {
		return
	}
	sbSrv.ReapExpiredFSSnapshots(ctx)
}

type snapshotRuntimeAPI interface {
	CaptureFilesystemSnapshot(ctx context.Context, req *cellarv1.CaptureFilesystemSnapshotRequest, opts ...grpc.CallOption) (*cellarv1.CaptureFilesystemSnapshotResponse, error)
	RestoreFilesystemSnapshot(ctx context.Context, req *cellarv1.RestoreFilesystemSnapshotRequest, opts ...grpc.CallOption) (*cellarv1.RestoreFilesystemSnapshotResponse, error)
	DeleteSnapshotBlob(ctx context.Context, req *cellarv1.DeleteSnapshotBlobRequest, opts ...grpc.CallOption) (*cellarv1.DeleteSnapshotBlobResponse, error)
}

func (d *Daemon) withNodeRuntime(ctx context.Context, nodeID string, fn func(client snapshotRuntimeAPI) error) error {
	mat := d.idStore.Material()
	if mat != nil && (nodeID == "" || nodeID == mat.NodeID) && d.driver != nil {
		return fn(&localRuntimeClient{d: d})
	}
	addr, err := d.lookupNodeRuntimeAddr(ctx, nodeID)
	if err != nil {
		return err
	}
	if mat == nil {
		return fmt.Errorf("no identity")
	}
	conn, err := grpcapi.DialRuntime(addr, mat.Certificate, mat.PrivateKey, mat.CACert)
	if err != nil {
		return err
	}
	defer conn.Close()
	return fn(cellarv1.NewSandboxRuntimeClient(conn))
}

// CaptureFilesystemSnapshot implements grpcapi.SnapshotNodeClient.
func (d *Daemon) CaptureFilesystemSnapshot(ctx context.Context, nodeID, sandboxID, snapshotID, location string) (string, int64, error) {
	var digest string
	var size int64
	err := d.withNodeRuntime(ctx, nodeID, func(client snapshotRuntimeAPI) error {
		resp, err := client.CaptureFilesystemSnapshot(ctx, &cellarv1.CaptureFilesystemSnapshotRequest{
			SandboxId:  sandboxID,
			SnapshotId: snapshotID,
			Location:   location,
		})
		if err != nil {
			return err
		}
		digest = resp.Digest
		size = resp.SizeBytes
		return nil
	})
	return digest, size, err
}

// RestoreFilesystemSnapshot implements grpcapi.SnapshotNodeClient.
func (d *Daemon) RestoreFilesystemSnapshot(ctx context.Context, nodeID, sandboxID, snapshotID, location string) error {
	return d.withNodeRuntime(ctx, nodeID, func(client snapshotRuntimeAPI) error {
		_, err := client.RestoreFilesystemSnapshot(ctx, &cellarv1.RestoreFilesystemSnapshotRequest{
			SandboxId:  sandboxID,
			SnapshotId: snapshotID,
			Location:   location,
		})
		return err
	})
}

// DeleteSnapshotBlob implements grpcapi.SnapshotNodeClient.
func (d *Daemon) DeleteSnapshotBlob(ctx context.Context, nodeID, location string) error {
	if d.blobs != nil && d.blobs.Backend() == string(sandbox.FSSnapshotBackendS3) {
		return d.blobs.Delete(ctx, location)
	}
	return d.withNodeRuntime(ctx, nodeID, func(client snapshotRuntimeAPI) error {
		_, err := client.DeleteSnapshotBlob(ctx, &cellarv1.DeleteSnapshotBlobRequest{Location: location})
		return err
	})
}

// localRuntimeClient calls the in-process driver so a single-node cluster
// does not need to dial its own SandboxRuntime.
type localRuntimeClient struct {
	d *Daemon
}

func (c *localRuntimeClient) CaptureFilesystemSnapshot(ctx context.Context, req *cellarv1.CaptureFilesystemSnapshotRequest, _ ...grpc.CallOption) (*cellarv1.CaptureFilesystemSnapshotResponse, error) {
	if c.d.driver == nil {
		return nil, fmt.Errorf("runtime not ready")
	}
	digest, size, err := c.d.driver.CaptureFS(ctx, req.SandboxId, req.SnapshotId, req.Location)
	if err != nil {
		return nil, err
	}
	return &cellarv1.CaptureFilesystemSnapshotResponse{Digest: digest, SizeBytes: size}, nil
}

func (c *localRuntimeClient) RestoreFilesystemSnapshot(ctx context.Context, req *cellarv1.RestoreFilesystemSnapshotRequest, _ ...grpc.CallOption) (*cellarv1.RestoreFilesystemSnapshotResponse, error) {
	if c.d.driver == nil {
		return nil, fmt.Errorf("runtime not ready")
	}
	if err := c.d.driver.RestoreFS(ctx, req.SnapshotId, req.Location); err != nil {
		return nil, err
	}
	return &cellarv1.RestoreFilesystemSnapshotResponse{}, nil
}

func (c *localRuntimeClient) DeleteSnapshotBlob(ctx context.Context, req *cellarv1.DeleteSnapshotBlobRequest, _ ...grpc.CallOption) (*cellarv1.DeleteSnapshotBlobResponse, error) {
	if c.d.driver == nil {
		return nil, fmt.Errorf("runtime not ready")
	}
	if err := c.d.driver.DeleteSnapshotObject(ctx, req.Location); err != nil {
		return nil, err
	}
	return &cellarv1.DeleteSnapshotBlobResponse{}, nil
}

func (d *Daemon) SandboxUpdate(ctx context.Context, req *cellarv1.SandboxUpdateRequest) (*cellarv1.SandboxUpdateResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		return sb.Update(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SandboxUpdateResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		conn, err := grpcapi.DialMTLS(addr, cert, key, ca)
		if err != nil {
			return err
		}
		defer conn.Close()
		var cerr error
		resp, cerr = cellarv1.NewSandboxControlClient(conn).Update(ctx, req)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SnapshotCreate(ctx context.Context, req *cellarv1.SnapshotCreateRequest) (*cellarv1.SnapshotCreateResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		return sb.CreateSnapshot(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SnapshotCreateResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		conn, err := grpcapi.DialMTLS(addr, cert, key, ca)
		if err != nil {
			return err
		}
		defer conn.Close()
		var cerr error
		resp, cerr = cellarv1.NewSandboxControlClient(conn).CreateSnapshot(ctx, req)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SnapshotGet(ctx context.Context, req *cellarv1.SnapshotGetRequest) (*cellarv1.SnapshotGetResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && sb != nil {
		return sb.GetSnapshot(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SnapshotGetResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		conn, err := grpcapi.DialMTLS(addr, cert, key, ca)
		if err != nil {
			return err
		}
		defer conn.Close()
		var cerr error
		resp, cerr = cellarv1.NewSandboxControlClient(conn).GetSnapshot(ctx, req)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SnapshotList(ctx context.Context, req *cellarv1.SnapshotListRequest) (*cellarv1.SnapshotListResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && sb != nil {
		return sb.ListSnapshots(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SnapshotListResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		conn, err := grpcapi.DialMTLS(addr, cert, key, ca)
		if err != nil {
			return err
		}
		defer conn.Close()
		var cerr error
		resp, cerr = cellarv1.NewSandboxControlClient(conn).ListSnapshots(ctx, req)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SnapshotDelete(ctx context.Context, req *cellarv1.SnapshotDeleteRequest) (*cellarv1.SnapshotDeleteResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		return sb.DeleteSnapshot(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SnapshotDeleteResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		conn, err := grpcapi.DialMTLS(addr, cert, key, ca)
		if err != nil {
			return err
		}
		defer conn.Close()
		var cerr error
		resp, cerr = cellarv1.NewSandboxControlClient(conn).DeleteSnapshot(ctx, req)
		return cerr
	})
	return resp, err
}
