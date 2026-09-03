package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/prodioslabs/cellar/internal/sandboxagent"
)

// FsEntry is a directory listing entry.
type FsEntry = sandboxagent.FsEntry

// FsMetadata is detailed file metadata.
type FsMetadata = sandboxagent.FsMetadata

// FsRead opens a streaming reader for a guest path.
func (a *Agent) FsRead(ctx context.Context, sandboxID, path string) (io.ReadCloser, error) {
	cid, err := a.resolveContainerID(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	cmd := []string{guestAgentBinPath, "fs", "read", path}
	sess, _, err := a.Driver.ExecSession(ctx, cid, cmd, false, false)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	var stderr bytes.Buffer
	go func() {
		defer sess.Close()
		_, copyErr := stdcopy.StdCopy(pw, &stderr, sess)
		code, errMsg := sess.Wait()
		if copyErr != nil && copyErr != io.EOF {
			_ = pw.CloseWithError(copyErr)
			return
		}
		if errMsg != "" {
			_ = pw.CloseWithError(fmt.Errorf("%s", errMsg))
			return
		}
		if code != 0 {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = fmt.Sprintf("exit code %d", code)
			}
			_ = pw.CloseWithError(fmt.Errorf("%s", msg))
			return
		}
		_ = pw.Close()
	}()
	return pr, nil
}

// FsWrite streams data from r into a guest path (create/overwrite).
func (a *Agent) FsWrite(ctx context.Context, sandboxID, path string, r io.Reader) error {
	cid, err := a.resolveContainerID(ctx, sandboxID)
	if err != nil {
		return err
	}
	cmd := []string{guestAgentBinPath, "fs", "write", path}
	sess, _, err := a.Driver.ExecSession(ctx, cid, cmd, false, true)
	if err != nil {
		return err
	}
	defer sess.Close()

	var stderr bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(io.Discard, &stderr, sess)
		errCh <- err
	}()

	if _, err := io.Copy(sess, r); err != nil {
		return err
	}
	_ = sess.CloseWrite()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if err != nil && err != io.EOF {
			return err
		}
	}
	code, errMsg := sess.Wait()
	if errMsg != "" {
		return fmt.Errorf("%s", errMsg)
	}
	if code != 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("exit code %d", code)
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// FsStat returns metadata for a guest path.
func (a *Agent) FsStat(ctx context.Context, sandboxID, path string) (*FsMetadata, error) {
	out, err := a.runAgentFsCollect(ctx, sandboxID, []string{guestAgentBinPath, "fs", "stat", path})
	if err != nil {
		return nil, err
	}
	var meta FsMetadata
	if err := json.Unmarshal([]byte(out), &meta); err != nil {
		return nil, fmt.Errorf("decode stat: %w", err)
	}
	return &meta, nil
}

// FsList lists entries in a guest directory.
func (a *Agent) FsList(ctx context.Context, sandboxID, path string) ([]FsEntry, error) {
	out, err := a.runAgentFsCollect(ctx, sandboxID, []string{guestAgentBinPath, "fs", "list", path})
	if err != nil {
		return nil, err
	}
	var entries []FsEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("decode list: %w", err)
	}
	return entries, nil
}

// FsExists reports whether a guest path exists.
func (a *Agent) FsExists(ctx context.Context, sandboxID, path string) (bool, error) {
	out, err := a.runAgentFsCollect(ctx, sandboxID, []string{guestAgentBinPath, "fs", "exists", path})
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected exists output %q", out)
	}
}

// FsMkdir creates a directory (parents must exist).
func (a *Agent) FsMkdir(ctx context.Context, sandboxID, path string) error {
	_, err := a.runAgentFsCollect(ctx, sandboxID, []string{guestAgentBinPath, "fs", "mkdir", path})
	return err
}

// FsRemove removes a file (not a directory).
func (a *Agent) FsRemove(ctx context.Context, sandboxID, path string) error {
	_, err := a.runAgentFsCollect(ctx, sandboxID, []string{guestAgentBinPath, "fs", "remove", path})
	return err
}

// FsRemoveDir removes an empty directory.
func (a *Agent) FsRemoveDir(ctx context.Context, sandboxID, path string) error {
	_, err := a.runAgentFsCollect(ctx, sandboxID, []string{guestAgentBinPath, "fs", "remove-dir", path})
	return err
}

// FsCopy copies a file within the sandbox.
func (a *Agent) FsCopy(ctx context.Context, sandboxID, from, to string) error {
	_, err := a.runAgentFsCollect(ctx, sandboxID, []string{guestAgentBinPath, "fs", "copy", from, to})
	return err
}

// FsRename renames or moves a path within the sandbox.
func (a *Agent) FsRename(ctx context.Context, sandboxID, from, to string) error {
	_, err := a.runAgentFsCollect(ctx, sandboxID, []string{guestAgentBinPath, "fs", "rename", from, to})
	return err
}

func (a *Agent) runAgentFsCollect(ctx context.Context, sandboxID string, cmd []string) (string, error) {
	cid, err := a.resolveContainerID(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	return a.runAgentJobCollect(ctx, cid, cmd)
}
