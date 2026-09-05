// Package version holds build-time identity shared by all Cellar binaries.
// Values are overridden at link time via -ldflags -X.
package version

import "fmt"

var (
	// Version is the SemVer string (e.g. "0.1.0"). Defaults to "dev" for
	// unstamped local builds.
	Version = "dev"
	// Commit is the short git commit SHA, or "none" when unavailable.
	Commit = "none"
	// Date is the UTC build timestamp (RFC3339), or "unknown" when unset.
	Date = "unknown"
)

// String returns a human-readable version line including commit and date.
func String() string {
	return fmt.Sprintf("%s (commit=%s date=%s)", Version, Commit, Date)
}

// Requested reports whether args contain -version or --version before a "--"
// terminator. Useful for binaries that need early exit before full flag/config
// parsing (e.g. cellard).
func Requested(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-version" || a == "--version" {
			return true
		}
	}
	return false
}
