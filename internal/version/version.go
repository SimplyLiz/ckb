// Package version provides centralized version information for CKB.
// This allows all packages to reference a single source of truth for version info.
package version

// These variables can be overridden at build time using ldflags:
// go build -ldflags "-X github.com/SimplyLiz/CodeMCP/internal/version.Version=1.0.0 -X github.com/SimplyLiz/CodeMCP/internal/version.Commit=abc123"
var (
	// Version is the semantic version of CKB
	Version = "9.3.1"

	// Commit is the git commit hash (set at build time)
	Commit = "unknown"

	// BuildDate is the build timestamp (set at build time)
	BuildDate = "unknown"
)

// Info returns a formatted version string
func Info() string {
	if Commit != "unknown" && len(Commit) > 7 {
		return Version + " (" + Commit[:7] + ")"
	}
	return Version
}

// Full returns complete version information
func Full() string {
	return "CKB version " + Version + "\n" +
		"Commit: " + Commit + "\n" +
		"Built: " + BuildDate
}
