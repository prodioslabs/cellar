//go:build !linux

package sandboxagent

import (
	"io/fs"
	"time"
)

func birthTime(info fs.FileInfo) *time.Time {
	// Best-effort: ModTime is not creation, so leave unavailable unless
	// platform-specific syscalls are added later.
	_ = info
	return nil
}
