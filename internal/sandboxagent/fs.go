package sandboxagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FsEntryKind matches the SDK/proto kind strings.
type FsEntryKind string

const (
	FsKindFile      FsEntryKind = "file"
	FsKindDirectory FsEntryKind = "directory"
	FsKindSymlink   FsEntryKind = "symlink"
	FsKindOther     FsEntryKind = "other"
)

// FsEntry is a directory listing entry (JSON on stdout for `fs list`).
type FsEntry struct {
	Path     string      `json:"path"`
	Kind     FsEntryKind `json:"kind"`
	Size     int64       `json:"size"`
	Mode     uint32      `json:"mode"`
	Modified *time.Time  `json:"modified"`
}

// FsMetadata is detailed metadata for `fs stat`.
type FsMetadata struct {
	Kind     FsEntryKind `json:"kind"`
	Size     int64       `json:"size"`
	Mode     uint32      `json:"mode"`
	Readonly bool        `json:"readonly"`
	Modified *time.Time  `json:"modified"`
	Created  *time.Time  `json:"created"`
}

// RunFsCLI dispatches `cellar-agent fs …` subcommands. Returns whether the
// args were handled as an fs command (true) so the caller can skip other modes.
func RunFsCLI(args []string) (handled bool, err error) {
	if len(args) < 1 || args[0] != "fs" {
		return false, nil
	}
	if len(args) < 2 {
		return true, fmt.Errorf("usage: cellar-agent fs <read|write|stat|list|mkdir|remove|remove-dir|exists|copy|rename> …")
	}
	switch args[1] {
	case "read":
		return true, fsRead(args[2:])
	case "write":
		return true, fsWrite(args[2:])
	case "stat":
		return true, fsStat(args[2:])
	case "list":
		return true, fsList(args[2:])
	case "mkdir":
		return true, fsMkdir(args[2:])
	case "remove":
		return true, fsRemove(args[2:])
	case "remove-dir":
		return true, fsRemoveDir(args[2:])
	case "exists":
		return true, fsExists(args[2:])
	case "copy":
		return true, fsCopy(args[2:])
	case "rename":
		return true, fsRename(args[2:])
	default:
		return true, fmt.Errorf("unknown fs subcommand %q", args[1])
	}
}

func validateAbsPath(path string) error {
	if path == "" {
		return fmt.Errorf("path required")
	}
	if strings.ContainsRune(path, 0) {
		return fmt.Errorf("path must not contain NUL")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %q", path)
	}
	return nil
}

func requireOnePath(args []string, usage string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("usage: cellar-agent fs %s", usage)
	}
	if err := validateAbsPath(args[0]); err != nil {
		return "", err
	}
	return args[0], nil
}

func requireTwoPaths(args []string, usage string) (from, to string, err error) {
	if len(args) != 2 {
		return "", "", fmt.Errorf("usage: cellar-agent fs %s", usage)
	}
	if err := validateAbsPath(args[0]); err != nil {
		return "", "", err
	}
	if err := validateAbsPath(args[1]); err != nil {
		return "", "", err
	}
	return args[0], args[1], nil
}

func kindFromMode(mode fs.FileMode) FsEntryKind {
	switch {
	case mode.IsRegular():
		return FsKindFile
	case mode.IsDir():
		return FsKindDirectory
	case mode&fs.ModeSymlink != 0:
		return FsKindSymlink
	default:
		return FsKindOther
	}
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func fsRead(args []string) error {
	path, err := requireOnePath(args, "read <path>")
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

func fsWrite(args []string) error {
	path, err := requireOnePath(args, "write <path>")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, os.Stdin); err != nil {
		return err
	}
	return f.Close()
}

func fsStat(args []string) error {
	path, err := requireOnePath(args, "stat <path>")
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	mode := info.Mode()
	meta := FsMetadata{
		Kind:     kindFromMode(mode),
		Size:     info.Size(),
		Mode:     uint32(mode.Perm()),
		Readonly: mode.Perm()&0o200 == 0,
		Modified: timePtr(info.ModTime()),
		Created:  birthTime(info),
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(meta)
}

func fsList(args []string) error {
	path, err := requireOnePath(args, "list <path>")
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	out := make([]FsEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		out = append(out, FsEntry{
			Path:     filepath.Join(path, e.Name()),
			Kind:     kindFromMode(mode),
			Size:     info.Size(),
			Mode:     uint32(mode.Perm()),
			Modified: timePtr(info.ModTime()),
		})
	}
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(out)
}

func fsMkdir(args []string) error {
	path, err := requireOnePath(args, "mkdir <path>")
	if err != nil {
		return err
	}
	return os.Mkdir(path, 0o755)
}

func fsRemove(args []string) error {
	path, err := requireOnePath(args, "remove <path>")
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory (use remove-dir): %s", path)
	}
	return os.Remove(path)
}

func fsRemoveDir(args []string) error {
	path, err := requireOnePath(args, "remove-dir <path>")
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	return os.Remove(path) // non-recursive; fails if not empty
}

func fsExists(args []string) error {
	path, err := requireOnePath(args, "exists <path>")
	if err != nil {
		return err
	}
	_, err = os.Lstat(path)
	if err == nil {
		fmt.Println("true")
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("false")
		return nil
	}
	return err
}

func fsCopy(args []string) error {
	from, to, err := requireTwoPaths(args, "copy <from> <to>")
	if err != nil {
		return err
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("copy does not support directories: %s", from)
	}
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Close()
}

func fsRename(args []string) error {
	from, to, err := requireTwoPaths(args, "rename <from> <to>")
	if err != nil {
		return err
	}
	return os.Rename(from, to)
}
