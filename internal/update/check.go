// Package update provides npm update checking for CKB.
// It checks if a newer version is available on npm and notifies the user.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/version"
)

const (
	// githubReleasesURL is the GitHub API endpoint for latest release
	githubReleasesURL = "https://api.github.com/repos/SimplyLiz/CodeMCP/releases/latest"

	// githubReleasesPage is the user-facing releases page
	githubReleasesPage = "https://github.com/SimplyLiz/CodeMCP/releases"

	// checkInterval is how often to check for updates (24 hours)
	checkInterval = 24 * time.Hour

	// httpTimeout is the timeout for the GitHub API request
	httpTimeout = 3 * time.Second

	// npmPackageName is the npm package name
	npmPackageName = "@tastehub/ckb"
)

// githubReleaseInfo represents the relevant fields from GitHub Releases API
type githubReleaseInfo struct {
	TagName string `json:"tag_name"`
}

// UpdateInfo contains information about an available update
type UpdateInfo struct {
	CurrentVersion string
	LatestVersion  string
	UpdateCommand  string
}

// Checker handles update checking with caching
type Checker struct {
	cache      *Cache
	isNpmPath  bool
	isBrewPath bool
}

// NewChecker creates a new update checker.
// It automatically detects if running from an npm installation.
func NewChecker() *Checker {
	return &Checker{
		cache:      NewCache(),
		isNpmPath:  detectNpmInstall(),
		isBrewPath: detectBrewInstall(),
	}
}

// detectNpmInstall checks if the current executable is running from an npm installation
func detectNpmInstall() bool {
	execPath, err := os.Executable()
	if err != nil {
		return false
	}

	// Resolve symlinks to get the real path
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath
	}

	// Check if path contains node_modules/@tastehub/ckb
	return strings.Contains(realPath, "node_modules") &&
		strings.Contains(realPath, "@tastehub") &&
		strings.Contains(realPath, "ckb")
}

// detectBrewInstall checks if the current executable is the Homebrew formula,
// which lives under <prefix>/Cellar/ckb/<version>/bin.
func detectBrewInstall() bool {
	execPath, err := os.Executable()
	if err != nil {
		return false
	}
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath
	}
	return isBrewPath(realPath)
}

func isBrewPath(p string) bool {
	return strings.Contains(filepath.ToSlash(p), "/Cellar/ckb/")
}

// IsNpmInstall returns true if running from an npm installation
func (c *Checker) IsNpmInstall() bool {
	return c.isNpmPath
}

// CheckCached checks the cache for a pending update notification.
// This is instant (no HTTP) and should be called at startup.
// Returns nil if no update available or cache is empty/stale.
func (c *Checker) CheckCached() *UpdateInfo {
	// Skip if disabled via environment variable
	if os.Getenv("CKB_NO_UPDATE_CHECK") != "" {
		return nil
	}

	// Read cache only - no network request
	cached, _ := c.cache.Get()
	if cached == nil {
		return nil
	}

	return c.compareVersions(cached.LatestVersion)
}

// RefreshCache fetches the latest version from GitHub and updates the cache.
// This should be called in a background goroutine.
func (c *Checker) RefreshCache(ctx context.Context) {
	// Skip if disabled via environment variable
	if os.Getenv("CKB_NO_UPDATE_CHECK") != "" {
		return
	}

	// Check if cache is still fresh
	_, needsRefresh := c.cache.Get()
	if !needsRefresh {
		return // Cache is fresh, no need to fetch
	}

	// Fetch latest version from GitHub
	latest := c.fetchLatestVersion(ctx)
	if latest != "" {
		c.cache.Set(latest)
	}
}

// Check checks for available updates (legacy method, combines cached + fetch).
// Returns nil if no update is available, check is disabled, or any error occurs.
// This method is designed to fail silently - it never returns an error.
func (c *Checker) Check(ctx context.Context) *UpdateInfo {
	// Skip if disabled via environment variable
	if os.Getenv("CKB_NO_UPDATE_CHECK") != "" {
		return nil
	}

	// Check cache first
	cached, needsRefresh := c.cache.Get()
	if cached != nil && !needsRefresh {
		return c.compareVersions(cached.LatestVersion)
	}

	// Fetch latest version from GitHub (with timeout)
	latest := c.fetchLatestVersion(ctx)
	if latest == "" {
		return nil
	}

	// Update cache
	c.cache.Set(latest)

	return c.compareVersions(latest)
}

// CheckAsync runs the update check in the background and returns results via channel.
// The channel will receive at most one result and then be closed.
func (c *Checker) CheckAsync(ctx context.Context) <-chan *UpdateInfo {
	ch := make(chan *UpdateInfo, 1)

	go func() {
		defer close(ch)
		if info := c.Check(ctx); info != nil {
			ch <- info
		}
	}()

	return ch
}

// fetchLatestVersion fetches the latest version from GitHub Releases API.
// Returns empty string on any error (silent failure).
func (c *Checker) fetchLatestVersion(ctx context.Context) string {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesURL, nil)
	if err != nil {
		return ""
	}

	// Set headers for GitHub API
	req.Header.Set("User-Agent", "github.com/SimplyLiz/CodeMCP/"+version.Version)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var release githubReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ""
	}

	// Strip 'v' prefix from tag if present (e.g., "v7.4.0" -> "7.4.0")
	tag := release.TagName
	if len(tag) > 0 && tag[0] == 'v' {
		tag = tag[1:]
	}

	return tag
}

// compareVersions compares current version with latest and returns UpdateInfo if update available
func (c *Checker) compareVersions(latest string) *UpdateInfo {
	current := version.Version

	// Simple semver comparison - assumes versions are in format X.Y.Z
	if !isNewerVersion(latest, current) {
		return nil
	}

	return &UpdateInfo{
		CurrentVersion: current,
		LatestVersion:  latest,
		UpdateCommand:  c.getUpgradeCommand(),
	}
}

// getUpgradeCommand returns the appropriate upgrade command based on install method
func (c *Checker) getUpgradeCommand() string {
	if c.isNpmPath {
		return "npm update -g " + npmPackageName
	}
	if c.isBrewPath {
		return "brew upgrade ckb"
	}
	return githubReleasesPage
}

// isNewerVersion returns true if version a is newer than version b.
// Handles semver format X.Y.Z with optional pre-release suffixes.
func isNewerVersion(a, b string) bool {
	partsA := parseVersion(a)
	partsB := parseVersion(b)

	for i := 0; i < 3; i++ {
		if partsA[i] > partsB[i] {
			return true
		}
		if partsA[i] < partsB[i] {
			return false
		}
	}

	return false
}

// parseVersion extracts major, minor, patch from a version string
func parseVersion(v string) [3]int {
	var parts [3]int

	// Strip any pre-release suffix (e.g., "-beta.1")
	if idx := strings.Index(v, "-"); idx > 0 {
		v = v[:idx]
	}

	// Parse X.Y.Z
	fmt.Sscanf(v, "%d.%d.%d", &parts[0], &parts[1], &parts[2])

	return parts
}

// FormatUpdateMessage formats the update notification for CLI output
func (u *UpdateInfo) FormatUpdateMessage() string {
	// Determine prefix based on whether it's a command or URL
	prefix := "Run: "
	if strings.HasPrefix(u.UpdateCommand, "http") {
		prefix = ""
	}

	cmdLine := prefix + u.UpdateCommand
	// Calculate padding (box is 53 chars inside)
	cmdPadding := 51 - len(cmdLine)
	if cmdPadding < 0 {
		cmdPadding = 0
	}

	verPadding := 21 - len(u.CurrentVersion) - len(u.LatestVersion)
	if verPadding < 0 {
		verPadding = 0
	}

	return fmt.Sprintf(
		"\n\033[33m╭─────────────────────────────────────────────────────╮\033[0m\n"+
			"\033[33m│\033[0m  Update available: \033[36m%s\033[0m → \033[32m%s\033[0m%s\033[33m│\033[0m\n"+
			"\033[33m│\033[0m  \033[1m%s\033[0m%s\033[33m│\033[0m\n"+
			"\033[33m╰─────────────────────────────────────────────────────╯\033[0m\n",
		u.CurrentVersion,
		u.LatestVersion,
		strings.Repeat(" ", verPadding),
		cmdLine,
		strings.Repeat(" ", cmdPadding),
	)
}

// FormatUpdateMessagePlain formats the update notification without colors
func (u *UpdateInfo) FormatUpdateMessagePlain() string {
	prefix := "Run: "
	if strings.HasPrefix(u.UpdateCommand, "http") {
		prefix = ""
	}
	return fmt.Sprintf(
		"\nUpdate available: %s → %s\n%s%s\n",
		u.CurrentVersion,
		u.LatestVersion,
		prefix,
		u.UpdateCommand,
	)
}
