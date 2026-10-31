package blob

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalStore writes objects under Root using the relative key as the path.
type LocalStore struct {
	Root string
}

// NewLocal returns a disk-backed store rooted at root.
func NewLocal(root string) (*LocalStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("local blob root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &LocalStore{Root: root}, nil
}

func (s *LocalStore) Backend() string { return "local" }

func (s *LocalStore) resolve(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("object key is required")
	}
	if strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	clean := filepath.Clean("/" + key)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	full := filepath.Join(s.Root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(s.Root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	return full, nil
}

// Put writes r to the key path, creating parent directories.
func (s *LocalStore) Put(_ context.Context, key string, r io.Reader) error {
	path, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, path)
}

// Get opens the object for reading.
func (s *LocalStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	path, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Delete removes the object. Missing keys are not an error.
func (s *LocalStore) Delete(_ context.Context, key string) error {
	path, err := s.resolve(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}
