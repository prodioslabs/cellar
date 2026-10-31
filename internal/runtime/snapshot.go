package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/prodioslabs/cellar/internal/blob"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

// SetBlobStore attaches the snapshot object store.
func (d *Driver) SetBlobStore(store blob.Store) {
	if d == nil {
		return
	}
	d.blobs = store
}

// CaptureFS snapshots a stopped VM and writes the tar to BlobStore at location.
func (d *Driver) CaptureFS(ctx context.Context, sandboxID, snapshotID, location string) (digest string, size int64, err error) {
	if sandboxID == "" || snapshotID == "" || location == "" {
		return "", 0, fmt.Errorf("sandbox_id, snapshot_id, and location are required")
	}
	if d.blobs == nil {
		return "", 0, fmt.Errorf("blob store is not configured")
	}
	_ = d.Stop(ctx, sandboxID, 30)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		phase, ierr := d.InspectPhase(ctx, sandboxID)
		if ierr != nil || phase != sandbox.PhaseRunning {
			break
		}
		if time.Now().After(deadline) {
			return "", 0, fmt.Errorf("sandbox %s still running; cannot snapshot", sandboxID)
		}
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	if _, err := msb.Snapshot.Create(ctx, msb.SnapshotCreateOptions{
		Name:            snapshotID,
		FromSandbox:     LocalName(sandboxID),
		RecordIntegrity: true,
	}); err != nil {
		return "", 0, fmt.Errorf("msb snapshot create: %w", err)
	}
	defer func() { _ = msb.Snapshot.Remove(context.Background(), snapshotID, true) }()

	tmp, err := os.CreateTemp("", "cellar-snapshot-*.tar")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if err := msb.Snapshot.Save(ctx, snapshotID, tmpPath, msb.SnapshotSaveOptions{PlainTar: true}); err != nil {
		return "", 0, fmt.Errorf("msb snapshot save: %w", err)
	}

	sum, n, err := hashFile(tmpPath)
	if err != nil {
		return "", 0, err
	}
	f, err := os.Open(tmpPath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	if err := d.blobs.Put(ctx, location, f); err != nil {
		return "", 0, fmt.Errorf("blob put %s: %w", location, err)
	}
	return sum, n, nil
}

// RestoreFS loads a snapshot tar from BlobStore into the local MSB store.
func (d *Driver) RestoreFS(ctx context.Context, snapshotID, location string) error {
	if snapshotID == "" || location == "" {
		return fmt.Errorf("snapshot_id and location are required")
	}
	if d.blobs == nil {
		return fmt.Errorf("blob store is not configured")
	}
	r, err := d.blobs.Get(ctx, location)
	if err != nil {
		return fmt.Errorf("blob get %s: %w", location, err)
	}
	defer r.Close()

	tmp, err := os.CreateTemp("", "cellar-restore-*.tar")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	defer os.Remove(tmpPath)

	destDir := filepath.Dir(tmpPath)
	if _, err := msb.Snapshot.Load(ctx, tmpPath, destDir); err != nil {
		// dest as snapshot name in the default store
		if _, err2 := msb.Snapshot.Load(ctx, tmpPath, snapshotID); err2 != nil {
			return fmt.Errorf("msb snapshot load: %v (named dest: %w)", err, err2)
		}
	}
	return nil
}

// DeleteSnapshotObject removes a tar from BlobStore.
func (d *Driver) DeleteSnapshotObject(ctx context.Context, location string) error {
	if d.blobs == nil {
		return fmt.Errorf("blob store is not configured")
	}
	return d.blobs.Delete(ctx, location)
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}
