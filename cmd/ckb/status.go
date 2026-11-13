package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/daemon"
	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/project"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/repos"
	"github.com/SimplyLiz/CodeMCP/internal/tier"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

var (
	statusFormat string
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show CKB system status",
	Long:  "Display the current status of CKB backends, cache, and repository state",
	Run:   runStatus,
}

func init() {
	statusCmd.Flags().StringVar(&statusFormat, "format", "human", "Output format (human, json)")
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) {
	start := time.Now()

	// Try to resolve active repo using the global registry
	resolved, err := repos.ResolveActiveRepo("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading registry: %v\n", err)
		os.Exit(1)
	}

	// If no active repo, show global status
	if resolved.Entry == nil {
		runGlobalStatus()
		return
	}

	// We have an active repo - show detailed status
	repoRoot := resolved.Entry.Path
	// Use silent logger - status output already displays all relevant info
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := mustGetEngine(repoRoot, logger)
	ctx := newContext()

	// Get status from Query Engine
	response, err := engine.GetStatus(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting status: %v\n", err)
		os.Exit(1)
	}

	// Convert to CLI response format
	cliResponse := convertStatusResponse(response)

	// Add active repo info
	cliResponse.ActiveRepo = &ActiveRepoCLI{
		Name:   resolved.Entry.Name,
		Path:   resolved.Entry.Path,
		Source: string(resolved.Source),
	}

	// Add daemon status
	cliResponse.DaemonStatus = getDaemonStatus()

	// Add index freshness status
	ckbDir := filepath.Join(repoRoot, ".ckb")
	cliResponse.IndexStatus = getIndexStatus(ckbDir, repoRoot)

	// Add change impact analysis status
	cliResponse.ChangeImpactStatus = getChangeImpactStatus(repoRoot)

	// Format and output
	output, err := FormatResponse(cliResponse, OutputFormat(statusFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)

	duration := time.Since(start).Milliseconds()
	if statusFormat == "human" {
		fmt.Printf("\n(Query took %dms)\n", duration)
	}

	// Touch last used timestamp (non-blocking, ignore errors)
	registry, _ := repos.LoadRegistry()
	if registry != nil {
		_ = registry.TouchLastUsed(resolved.Entry.Name)
	}
}

// runGlobalStatus shows status when no active repo is set
func runGlobalStatus() {
	fmt.Printf("CKB %s\n\n", version.Version)

	registry, err := repos.LoadRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading registry: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Active repo: (none)")
	fmt.Println()

	entries := registry.List()
	if len(entries) == 0 {
		fmt.Println("No repositories registered.")
		fmt.Println()
		fmt.Println("Get started:")
		fmt.Println("  cd /path/to/project && ckb init")
		return
	}

	// Sort by last used (most recent first), then by name
	repos.SortByLastUsed(entries)

	fmt.Println("Registered projects:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	for i, e := range entries {
		state := registry.ValidateState(e.Name)
		stateIndicator := ""
		if state != repos.RepoStateValid {
			stateIndicator = fmt.Sprintf(" (%s)", state)
		}

		lastUsed := ""
		if !e.LastUsedAt.IsZero() {
			lastUsed = formatRelativeTime(e.LastUsedAt)
		}

		fmt.Fprintf(w, "  %d. %s\t%s\t%s%s\n", i+1, e.Name, e.Path, lastUsed, stateIndicator)
	}
	w.Flush()

	fmt.Println()
	fmt.Println("Use 'ckb use <name>' to switch projects.")
	fmt.Println("Use 'ckb init' to initialize current directory as a new project.")
}

// ActiveRepoCLI describes the currently active repository
type ActiveRepoCLI struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Source string `json:"source"` // How the repo was resolved: env, flag, cwd, default
}

// DaemonStatusCLI describes the daemon's running state
type DaemonStatusCLI struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	Port    int    `json:"port,omitempty"`
	Uptime  string `json:"uptime,omitempty"`
}

// StatusResponseCLI contains the complete system status for CLI output
type StatusResponseCLI struct {
	CkbVersion         string                 `json:"ckbVersion"`
	ActiveRepo         *ActiveRepoCLI         `json:"activeRepo,omitempty"`
	DaemonStatus       *DaemonStatusCLI       `json:"daemonStatus,omitempty"`
	Tier               *tier.TierInfo         `json:"tier"`
	RepoState          *query.RepoState       `json:"repoState"`
	IndexStatus        *IndexStatusCLI        `json:"indexStatus,omitempty"`
	ChangeImpactStatus *ChangeImpactStatusCLI `json:"changeImpactStatus,omitempty"`
	Backends           []BackendStatusCLI     `json:"backends"`
	Cache              CacheStatusCLI         `json:"cache"`
	Healthy            bool                   `json:"healthy"`
}

// ChangeImpactStatusCLI describes the availability of change impact analysis features
type ChangeImpactStatusCLI struct {
	Coverage   *CoverageStatusCLI   `json:"coverage,omitempty"`
	Codeowners *CodeownersStatusCLI `json:"codeowners,omitempty"`
	Language   string               `json:"language,omitempty"`
}

// CoverageStatusCLI describes coverage file status
type CoverageStatusCLI struct {
	Found       bool      `json:"found"`
	Path        string    `json:"path,omitempty"`
	Age         string    `json:"age,omitempty"`
	ModTime     time.Time `json:"modTime,omitempty"`
	Stale       bool      `json:"stale,omitempty"`
	GenerateCmd string    `json:"generateCmd,omitempty"`
}

// CodeownersStatusCLI describes CODEOWNERS file status
type CodeownersStatusCLI struct {
	Found        bool   `json:"found"`
	Path         string `json:"path,omitempty"`
	TeamCount    int    `json:"teamCount,omitempty"`
	PatternCount int    `json:"patternCount,omitempty"`
}

// IndexStatusCLI describes the state of the SCIP index
type IndexStatusCLI struct {
	Exists         bool      `json:"exists"`
	Fresh          bool      `json:"fresh"`
	Reason         string    `json:"reason,omitempty"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
	IndexAge       string    `json:"indexAge,omitempty"`
	CommitHash     string    `json:"commitHash,omitempty"`
	FileCount      int       `json:"fileCount,omitempty"`
	CommitsBehind  int       `json:"commitsBehind,omitempty"`
	HasUncommitted bool      `json:"hasUncommitted,omitempty"`
	// Languages are the languages in the index; Skipped the ones it leaves
	// out, with why and the fix.
	Languages []index.LanguageIndex   `json:"languages,omitempty"`
	Skipped   []index.SkippedLanguage `json:"skippedLanguages,omitempty"`
}

// BackendStatusCLI describes the status of a backend
type BackendStatusCLI struct {
	ID           string   `json:"id"`
	Available    bool     `json:"available"`
	Healthy      bool     `json:"healthy"`
	Capabilities []string `json:"capabilities"`
	Details      string   `json:"details,omitempty"`
}

// CacheStatusCLI describes the cache state
type CacheStatusCLI struct {
	QueryCount int     `json:"queryCount"`
	ViewCount  int     `json:"viewCount"`
	HitRate    float64 `json:"hitRate"`
	SizeBytes  int64   `json:"sizeBytes"`
}

func convertStatusResponse(resp *query.StatusResponse) *StatusResponseCLI {
	backends := make([]BackendStatusCLI, 0, len(resp.Backends))
	for _, b := range resp.Backends {
		backends = append(backends, BackendStatusCLI{
			ID:           b.Id,
			Available:    b.Available,
			Healthy:      b.Healthy,
			Capabilities: b.Capabilities,
			Details:      b.Details,
		})
	}

	var cache CacheStatusCLI
	if resp.Cache != nil {
		cache = CacheStatusCLI{
			QueryCount: resp.Cache.QueriesCached,
			ViewCount:  resp.Cache.ViewsCached,
			HitRate:    resp.Cache.HitRate,
			SizeBytes:  resp.Cache.SizeBytes,
		}
	}

	return &StatusResponseCLI{
		CkbVersion: resp.CkbVersion,
		Tier:       resp.Tier,
		RepoState:  resp.RepoState,
		Backends:   backends,
		Cache:      cache,
		Healthy:    resp.Healthy,
	}
}

// getIndexStatus retrieves index freshness information
func getIndexStatus(ckbDir, repoRoot string) *IndexStatusCLI {
	// Respect configured index path instead of hardcoding index.scip
	indexPath := filepath.Join(repoRoot, "index.scip")
	if cfg, loadErr := config.LoadConfig(repoRoot); loadErr == nil && cfg.Backends.Scip.IndexPath != "" {
		cfgPath := cfg.Backends.Scip.IndexPath
		if filepath.IsAbs(cfgPath) {
			indexPath = cfgPath
		} else {
			indexPath = filepath.Join(repoRoot, cfgPath)
		}
	}

	// Check if index file exists
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		return &IndexStatusCLI{
			Exists: false,
			Fresh:  false,
			Reason: "no index found - run 'ckb index' to create one",
		}
	}

	// Load index metadata
	meta, err := index.LoadMeta(ckbDir)
	if err != nil {
		return &IndexStatusCLI{
			Exists: true,
			Fresh:  false,
			Reason: "could not read index metadata",
		}
	}

	if meta == nil {
		return &IndexStatusCLI{
			Exists: true,
			Fresh:  false,
			Reason: "legacy index - run 'ckb index' to enable freshness tracking",
		}
	}

	// Check freshness and staleness
	freshness := meta.CheckFreshness(repoRoot)
	staleness := meta.GetStaleness(repoRoot)

	return &IndexStatusCLI{
		Exists:         true,
		Fresh:          freshness.Fresh,
		Reason:         freshness.Reason,
		CreatedAt:      meta.CreatedAt,
		IndexAge:       staleness.IndexAge,
		CommitHash:     meta.CommitHash,
		FileCount:      meta.FileCount,
		CommitsBehind:  freshness.CommitsBehind,
		HasUncommitted: freshness.HasUncommitted,
		Languages:      meta.Languages,
		Skipped:        meta.Skipped,
	}
}

// getChangeImpactStatus detects coverage files and CODEOWNERS for change impact analysis
func getChangeImpactStatus(repoRoot string) *ChangeImpactStatusCLI {
	status := &ChangeImpactStatusCLI{}

	// Detect language
	lang, _, ok := project.DetectLanguage(repoRoot)
	if ok {
		status.Language = string(lang)
	}

	// Load config for coverage settings
	cfg, _ := config.LoadConfig(repoRoot)
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	// Detect coverage file (using config)
	status.Coverage = detectCoverage(repoRoot, lang, &cfg.Coverage)

	// Detect CODEOWNERS
	status.Codeowners = detectCodeowners(repoRoot)

	return status
}

// coverageLocation describes a coverage file location by language
type coverageLocation struct {
	paths       []string
	generateCmd string
}

// coverageByLang maps languages to their coverage file locations and generation commands
var coverageByLang = map[project.Language]coverageLocation{
	project.LangGo: {
		paths:       []string{"coverage.out", "coverage.txt", "cover.out"},
		generateCmd: "go test -coverprofile=coverage.out ./...",
	},
	project.LangDart: {
		paths:       []string{"coverage/lcov.info"},
		generateCmd: "flutter test --coverage",
	},
	project.LangTypeScript: {
		paths:       []string{"coverage/lcov.info", "coverage/coverage-final.json"},
		generateCmd: "npm test -- --coverage",
	},
	project.LangJavaScript: {
		paths:       []string{"coverage/lcov.info", "coverage/coverage-final.json"},
		generateCmd: "npm test -- --coverage",
	},
	project.LangPython: {
		paths:       []string{".coverage", "coverage.xml", "htmlcov/index.html"},
		generateCmd: "pytest --cov=. --cov-report=xml",
	},
	project.LangRust: {
		paths:       []string{"target/coverage/lcov.info", "tarpaulin-report.json"},
		generateCmd: "cargo tarpaulin --out Lcov",
	},
	project.LangJava: {
		paths:       []string{"target/site/jacoco/jacoco.xml", "build/reports/jacoco/test/jacocoTestReport.xml"},
		generateCmd: "mvn test jacoco:report",
	},
}

// genericCoveragePaths are checked for any language
var genericCoveragePaths = []string{
	"lcov.info",
	"coverage/lcov.info",
	".coverage",
}

// detectCoverage looks for coverage files in the repository
func detectCoverage(repoRoot string, lang project.Language, coverageCfg *config.CoverageConfig) *CoverageStatusCLI {
	status := &CoverageStatusCLI{Found: false}

	var paths []string
	var generateCmd string

	// Use custom paths from config first
	if coverageCfg != nil && len(coverageCfg.Paths) > 0 {
		paths = append(paths, coverageCfg.Paths...)
	}

	// Add auto-detected paths if enabled (default: true)
	if coverageCfg == nil || coverageCfg.AutoDetect {
		if loc, ok := coverageByLang[lang]; ok {
			paths = append(paths, loc.paths...)
			generateCmd = loc.generateCmd
		}
		// Add generic paths
		paths = append(paths, genericCoveragePaths...)
	}

	// Parse max age from config (default: 168h = 7 days)
	maxAge := 7 * 24 * time.Hour
	if coverageCfg != nil && coverageCfg.MaxAge != "" {
		if parsed, err := time.ParseDuration(coverageCfg.MaxAge); err == nil {
			maxAge = parsed
		}
	}

	// Check each path
	for _, relPath := range paths {
		fullPath := filepath.Join(repoRoot, relPath)
		info, err := os.Stat(fullPath)
		if err == nil && !info.IsDir() {
			status.Found = true
			status.Path = relPath
			status.ModTime = info.ModTime()
			status.Age = formatDuration(time.Since(info.ModTime()))

			// Check if stale based on config max age
			if time.Since(info.ModTime()) > maxAge {
				status.Stale = true
			}
			break
		}
	}

	// Set generation command if not found
	if !status.Found && generateCmd != "" {
		status.GenerateCmd = generateCmd
	}

	return status
}

// codeownersLocations are the standard CODEOWNERS file locations
var codeownersLocations = []string{
	".github/CODEOWNERS",
	"CODEOWNERS",
	"docs/CODEOWNERS",
}

// detectCodeowners looks for CODEOWNERS file and parses basic stats
func detectCodeowners(repoRoot string) *CodeownersStatusCLI {
	status := &CodeownersStatusCLI{Found: false}

	for _, relPath := range codeownersLocations {
		fullPath := filepath.Join(repoRoot, relPath)
		content, err := os.ReadFile(fullPath) // #nosec G703 -- path is internally constructed
		if err == nil {
			status.Found = true
			status.Path = relPath

			// Parse basic stats
			lines := strings.Split(string(content), "\n")
			teams := make(map[string]bool)

			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				status.PatternCount++

				// Extract team mentions (@org/team or @username)
				parts := strings.Fields(line)
				for _, part := range parts[1:] { // Skip the pattern
					if strings.HasPrefix(part, "@") {
						teams[part] = true
					}
				}
			}
			status.TeamCount = len(teams)
			break
		}
	}

	return status
}

// formatDuration formats a duration in human-readable form
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes()) // #nosec G115 -- duration fits in int
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours()) // #nosec G115 -- duration fits in int
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	days := int(d.Hours() / 24) // #nosec G115 -- duration fits in int
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

// getDaemonStatus checks if the daemon is running and gets its status
func getDaemonStatus() *DaemonStatusCLI {
	running, pid, err := daemon.IsRunning()
	if err != nil || !running {
		return &DaemonStatusCLI{Running: false}
	}

	status := &DaemonStatusCLI{
		Running: true,
		PID:     pid,
		Port:    config.DefaultDaemonPort,
	}

	// Try to get more info from the daemon's health endpoint
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/health", config.DefaultDaemonPort))
	if err != nil {
		return status
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var health daemon.HealthResponse
		if err := json.NewDecoder(resp.Body).Decode(&health); err == nil {
			status.Uptime = health.Uptime
		}
	}

	return status
}
