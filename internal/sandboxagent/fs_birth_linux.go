package sandboxagent

import (
	"io/fs"
	"time"
)

// birthTime returns the file birth/creation time when available.
// On Linux this is typically unavailable; callers treat nil as unknown.
func birthTime(_ fs.FileInfo) *time.Time {
	return nil
}
