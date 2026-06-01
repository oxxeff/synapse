// Package version provides build-time version information injected via ldflags.
//
// The package is intentionally self-contained and dependency-free so it can be
// lifted into a standalone library later without changes. Variables are
// populated at compile time:
//
//	go build -ldflags "\
//	  -X go.oxef.dev/ci/synapse/internal/version.Version=1.0.0 \
//	  -X go.oxef.dev/ci/synapse/internal/version.Commit=$(git rev-parse HEAD) \
//	  -X go.oxef.dev/ci/synapse/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
//	  -X go.oxef.dev/ci/synapse/internal/version.Build=42"
package version

import (
	"fmt"
	"strings"
)

var (
	// Version is the semantic version (e.g. "1.0.0"). Overridden by ldflags.
	Version = "dev"

	// Commit is the full git commit hash. Overridden by ldflags.
	Commit = "none"

	// Date is the build date in RFC3339 format. Overridden by ldflags.
	Date = "unknown"

	// Build is the CI build number. Overridden by ldflags.
	Build = "0"
)

// Human returns a human-readable version string.
//
// Format: 1.0.0-a1b2c3d (#42, 2026-02-01)
func Human() string {
	return fmt.Sprintf("%s-%s (#%s, %s)", Version, shortCommit(), Build, Date)
}

// Technical returns a semver-compatible version string with build metadata.
//
// Format: 1.0.0+42.20260201.a1b2c3d
func Technical() string {
	cleanDate := strings.ReplaceAll(Date, "-", "")

	return fmt.Sprintf("%s+%s.%s.%s", Version, Build, cleanDate, shortCommit())
}

func shortCommit() string {
	if len(Commit) > 7 {
		return Commit[:7]
	}

	return Commit
}
