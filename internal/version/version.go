package version

import "fmt"

// These are set at build time via -ldflags.
var (
	// Version is the semver release tag (e.g. "v1.2.3") or "dev" for local builds.
	Version = "dev"
	// Commit is the short git SHA of the build.
	Commit = "none"
	// BuildDate is the UTC timestamp of the build in RFC3339 format.
	BuildDate = "unknown"
)

// String returns a human-readable version string combining Version, Commit, and BuildDate.
func String() string {
	return fmt.Sprintf("%s (commit=%s, built=%s)", Version, Commit, BuildDate)
}
