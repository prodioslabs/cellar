package blob

import (
	"context"
	"io"
)

// Store is a content-addressed-by-key object store for snapshot tars.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Backend() string
}

// Config selects the blob backend at cellard start.
type Config struct {
	// DataDir is the local root (default cellard data-dir). Keys are appended.
	DataDir string
	// SnapshotDir overrides the local root when set.
	SnapshotDir string
	// Bucket selects the S3 backend when non-empty.
	Bucket   string
	Region   string
	Prefix   string
	Endpoint string
}

// LocalRoot returns the filesystem root for the local backend.
func (c Config) LocalRoot() string {
	if c.SnapshotDir != "" {
		return c.SnapshotDir
	}
	return c.DataDir
}
