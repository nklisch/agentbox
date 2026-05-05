package version

import "fmt"

// Set via -ldflags at build time. Defaults are dev fallbacks.
var (
	Version = "0.0.1-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns the human-readable version string used by --version.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
