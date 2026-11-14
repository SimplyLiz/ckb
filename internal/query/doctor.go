package query

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/lip"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// DoctorResponse is the response for the doctor command.
type DoctorResponse struct {
	Healthy         bool          `json:"healthy"`
	Checks          []DoctorCheck `json:"checks"`
	QueryDurationMs int64         `json:"queryDurationMs"`
}

// DoctorCheck represents a single diagnostic check.
type DoctorCheck struct {
	Name           string      `json:"name"`
	Status         string      `json:"status"` // "pass", "warn", "fail"
	Message        string      `json:"message"`
	SuggestedFixes []FixAction `json:"suggestedFixes,omitempty"`
}

// FixAction describes a suggested fix.
type FixAction struct {
	Type        string   `json:"type"` // "run-command", "open-docs", "install-tool"
	Command     string   `json:"command,omitempty"`
	Safe        bool     `json:"safe"`
	Description string   `json:"description"`
	URL         string   `json:"url,omitempty"`
	Tool        string   `json:"tool,omitempty"`
	Methods     []string `json:"methods,omitempty"`
}

// Doctor runs diagnostic checks.
func (e *Engine) Doctor(ctx context.Context, checkName string) (*DoctorResponse, error) {
	startTime := time.Now()
	checks := make([]DoctorCheck, 0)
	healthy := true

	// Run all checks or specific one
	if checkName == "" || checkName == "all" {
		checks = append(checks, e.checkGit(ctx))
		checks = append(checks, e.checkScip(ctx))
		checks = append(checks, e.checkLsp(ctx))
		checks = append(checks, e.checkLIP(ctx))
		checks = append(checks, e.checkConfig(ctx))
		checks = append(checks, e.checkStorage(ctx))
		checks = append(checks, e.checkOrphanedIndexes(ctx))
		checks = append(checks, e.checkOptionalTools(ctx))
	} else {
		switch checkName {
		case "git":
			checks = append(checks, e.checkGit(ctx))
		case "scip":
			checks = append(checks, e.checkScip(ctx))
		case "lsp":
			checks = append(checks, e.checkLsp(ctx))
		case "lip":
			checks = append(checks, e.checkLIP(ctx))
		case "config":
			checks = append(checks, e.checkConfig(ctx))
		case "storage":
			checks = append(checks, e.checkStorage(ctx))
		case "orphaned":
			checks = append(checks, e.checkOrphanedIndexes(ctx))
		case "optional":
			checks = append(checks, e.checkOptionalTools(ctx))
		default:
			checks = append(checks, DoctorCheck{
				Name:    checkName,
				Status:  "fail",
				Message: fmt.Sprintf("Unknown check: %s", checkName),
			})
		}
	}

	// Determine overall health
	for _, check := range checks {
		if check.Status == "fail" {
			healthy = false
			break
		}
	}

	return &DoctorResponse{
		Healthy:         healthy,
		Checks:          checks,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}, nil
}

// checkGit verifies git is available and repo is valid.
func (e *Engine) checkGit(ctx context.Context) DoctorCheck {
	check := DoctorCheck{
		Name: "git",
	}

	if e.gitAdapter == nil {
		check.Status = "fail"
		check.Message = "Git backend not initialized"
		check.SuggestedFixes = []FixAction{
			{
				Type:        "install-tool",
				Tool:        "git",
				Description: "Install git",
				Methods:     []string{"brew", "apt", "manual"},
			},
		}
		return check
	}

	if !e.gitAdapter.IsAvailable() {
		check.Status = "fail"
		check.Message = "Not a git repository"
		check.SuggestedFixes = []FixAction{
			{
				Type:        "run-command",
				Command:     "git init",
				Safe:        true,
				Description: "Initialize git repository",
			},
		}
		return check
	}

	// Check if repo state can be computed
	state, err := e.gitAdapter.GetRepoState()
	if err != nil {
		check.Status = "warn"
		check.Message = fmt.Sprintf("Git available but repo state error: %s", err.Error())
		return check
	}

	check.Status = "pass"
	if state.Dirty {
		commitShort := state.HeadCommit
		if len(commitShort) > 12 {
			commitShort = commitShort[:12]
		}
		check.Message = fmt.Sprintf("Git repository at %s (uncommitted changes)", commitShort)
	} else {
		commitShort := state.HeadCommit
		if len(commitShort) > 12 {
			commitShort = commitShort[:12]
		}
		check.Message = fmt.Sprintf("Git repository at %s", commitShort)
	}

	return check
}

// scipLargeRepoThreshold matches the threshold in cmd/ckb/index.go.
// Repos above this size skip SCIP by default and use LSP+LIP instead.
const scipLargeRepoThreshold = 50_000

// checkScip verifies SCIP index availability.
func (e *Engine) checkScip(ctx context.Context) DoctorCheck {
	check := DoctorCheck{
		Name: "scip",
	}

	if e.scipAdapter == nil {
		check.Status = "warn"
		check.Message = "SCIP index not configured — run 'ckb init' to set up"
		check.SuggestedFixes = e.getSCIPInstallSuggestions()
		return check
	}

	if !e.scipAdapter.IsAvailable() {
		// Check whether this looks like a large repo that intentionally skipped SCIP.
		lang, _, _ := project.DetectLanguage(e.repoRoot)
		if lang != "" && countDoctorSourceFiles(e.repoRoot, lang) >= scipLargeRepoThreshold {
			check.Status = "pass"
			check.Message = "SCIP disabled — repo exceeds 50k source files. " +
				"Active tier: FTS + LSP + LIP semantic search. " +
				"Call graph and analyzeImpact require SCIP (run: ckb index --scip)"
			check.SuggestedFixes = []FixAction{
				{
					Type:        "run-command",
					Command:     "ckb index --scip",
					Safe:        true,
					Description: "Generate SCIP index (may take 30–90 min on large repos)",
				},
			}
			return check
		}
		check.Status = "warn"
		check.Message = e.scipNotFoundMessage()
		check.SuggestedFixes = e.getSCIPInstallSuggestions()
		return check
	}

	// Check index info
	info := e.scipAdapter.GetIndexInfo()
	if info == nil {
		check.Status = "warn"
		check.Message = "SCIP index info unavailable"
		return check
	}

	if info.Freshness != nil && info.Freshness.IsStale() {
		// Dirty-only staleness (index is at HEAD, only uncommitted changes exist)
		// is expected during active development — report as info, not warn.
		if !info.Freshness.StaleAgainstHead && info.Freshness.StaleAgainstRepoState {
			check.Status = "info"
			check.Message = "SCIP index at HEAD — uncommitted changes may not be reflected"
			return check
		}
		check.Status = "warn"
		check.Message = fmt.Sprintf("SCIP index is stale: %s", info.Freshness.Warning)
		check.SuggestedFixes = []FixAction{
			{
				Type:        "run-command",
				Command:     e.detectSCIPCommand(),
				Safe:        true,
				Description: "Regenerate SCIP index",
			},
		}
		return check
	}

	check.Status = "pass"
	check.Message = fmt.Sprintf("SCIP index available (%d symbols)", info.SymbolCount)
	return check
}

// checkLsp verifies LSP servers are configured and their commands are available.
// Only checks servers relevant to the detected project languages.
func (e *Engine) checkLsp(ctx context.Context) DoctorCheck {
	check := DoctorCheck{
		Name: "lsp",
	}

	if e.lspSupervisor == nil || e.config == nil {
		check.Status = "info"
		check.Message = "LSP not configured"
		return check
	}

	servers := e.config.Backends.Lsp.Servers
	if len(servers) == 0 {
		check.Status = "info"
		check.Message = "No LSP servers configured"
		return check
	}

	// Detect project languages to filter relevant LSP servers
	relevantServers := e.filterRelevantLspServers(servers)

	// Test each relevant server command
	var available, missing []string
	var fixes []FixAction

	for lang, cfg := range relevantServers {
		if _, err := exec.LookPath(cfg.Command); err == nil {
			available = append(available, lang)
		} else {
			missing = append(missing, lang)
			fixes = append(fixes, e.getLspInstallFix(lang, cfg.Command))
		}
	}

	// Sort for consistent output
	sort.Strings(available)
	sort.Strings(missing)

	if len(missing) == 0 {
		check.Status = "pass"
		check.Message = fmt.Sprintf("LSP ready: %s (starts on-demand)",
			strings.Join(available, ", "))
	} else {
		var parts []string
		for _, lang := range missing {
			cfg := relevantServers[lang]
			fix := e.getLspInstallFix(lang, cfg.Command)
			if fix.Command != "" {
				parts = append(parts, fmt.Sprintf("LSP not installed for %s — install %s: %s",
					lang, cfg.Command, fix.Command))
			} else {
				parts = append(parts, fmt.Sprintf("LSP not installed for %s — install %s",
					lang, cfg.Command))
			}
		}
		check.Message = strings.Join(parts, "; ")
		check.SuggestedFixes = fixes

		// Downgrade to info when the primary language has LSP coverage and only
		// secondary/incidental languages are missing.
		primaryLang, _, _ := project.DetectLanguage(e.repoRoot)
		primaryServerKey := langToLspServer[primaryLang]
		if primaryServerKey != "" {
			primaryAvailable := false
			for _, lang := range available {
				if lang == primaryServerKey {
					primaryAvailable = true
					break
				}
			}
			if primaryAvailable {
				check.Status = "info"
			} else {
				check.Status = "warn"
			}
		} else {
			check.Status = "warn"
		}
	}

	return check
}

// langToLspServer maps project.Language values to LSP server config keys.
var langToLspServer = map[project.Language]string{
	project.LangGo:         "go",
	project.LangTypeScript: "typescript",
	project.LangJavaScript: "typescript", // JS uses typescript-language-server
	project.LangPython:     "python",
	project.LangDart:       "dart",
	project.LangRust:       "rust",
	project.LangJava:       "java",
	project.LangKotlin:     "kotlin",
	project.LangCpp:        "cpp",
	project.LangRuby:       "ruby",
	project.LangCSharp:     "csharp",
	project.LangPHP:        "php",
}

// filterRelevantLspServers returns only the LSP servers that match detected
// project languages. Falls back to all servers if no languages are detected.
func (e *Engine) filterRelevantLspServers(servers map[string]config.LspServerCfg) map[string]config.LspServerCfg {
	_, _, allLangs := project.DetectAllLanguages(e.repoRoot)
	if len(allLangs) == 0 {
		return servers // Fall back to checking all
	}

	// Build set of relevant LSP server keys
	relevant := make(map[string]bool)
	for _, lang := range allLangs {
		if serverKey, ok := langToLspServer[lang]; ok {
			relevant[serverKey] = true
		}
	}

	filtered := make(map[string]config.LspServerCfg)
	for key, cfg := range servers {
		if relevant[key] {
			filtered[key] = cfg
		}
	}

	if len(filtered) == 0 {
		return servers // Fall back if no configured servers match
	}
	return filtered
}

// getLspInstallFix returns installation instructions for an LSP server command.
func (e *Engine) getLspInstallFix(lang, command string) FixAction {
	switch command {
	case "gopls":
		return FixAction{
			Type:        "run-command",
			Command:     "go install golang.org/x/tools/gopls@latest",
			Safe:        true,
			Description: fmt.Sprintf("Install %s for %s", command, lang),
		}
	case "typescript-language-server":
		return FixAction{
			Type:        "run-command",
			Command:     "npm install -g typescript-language-server typescript",
			Safe:        true,
			Description: fmt.Sprintf("Install %s for %s", command, lang),
		}
	case "dart":
		return FixAction{
			Type:        "open-docs",
			URL:         "https://dart.dev/get-dart",
			Description: fmt.Sprintf("Install Dart SDK for %s LSP", lang),
		}
	case "pylsp":
		return FixAction{
			Type:        "run-command",
			Command:     "pip install python-lsp-server",
			Safe:        true,
			Description: fmt.Sprintf("Install %s for %s", command, lang),
		}
	default:
		return FixAction{
			Type:        "install-tool",
			Tool:        command,
			Description: fmt.Sprintf("Install %s for %s", command, lang),
		}
	}
}

// checkConfig verifies configuration.
func (e *Engine) checkConfig(ctx context.Context) DoctorCheck {
	check := DoctorCheck{
		Name: "config",
	}

	configPath := filepath.Join(e.repoRoot, ".ckb", "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		check.Status = "warn"
		check.Message = "No .ckb/config.json found (using defaults)"
		check.SuggestedFixes = []FixAction{
			{
				Type:        "run-command",
				Command:     "ckb init",
				Safe:        true,
				Description: "Initialize CKB configuration",
			},
		}
		return check
	}

	check.Status = "pass"
	check.Message = "Configuration file found"
	return check
}

// checkStorage verifies storage layer.
func (e *Engine) checkStorage(ctx context.Context) DoctorCheck {
	check := DoctorCheck{
		Name: "storage",
	}

	if e.db == nil {
		check.Status = "fail"
		check.Message = "Database not initialized"
		return check
	}

	// Try a simple query to check DB is working (schema_version is always present)
	var schemaVersion int
	row := e.db.QueryRow("SELECT version FROM schema_version LIMIT 1")
	if err := row.Scan(&schemaVersion); err != nil {
		check.Status = "warn"
		check.Message = "Database schema not initialized — run 'ckb index' to populate"
		return check
	}

	// Count symbols in FTS index as a health indicator
	var count int
	_ = e.db.QueryRow("SELECT COUNT(*) FROM symbols_fts_content").Scan(&count)

	check.Status = "pass"
	check.Message = fmt.Sprintf("Database OK (schema v%d, %d symbols indexed)", schemaVersion, count)
	return check
}

// scipNotFoundMessage returns a contextual "SCIP index not found" message
// based on the detected project language.
func (e *Engine) scipNotFoundMessage() string {
	lang, _, ok := project.DetectLanguage(e.repoRoot)
	if !ok {
		return "SCIP index not found — run 'ckb index' to generate"
	}
	indexer := project.GetIndexerConfig(lang)
	if indexer == nil {
		return "SCIP index not found — run 'ckb index' to generate"
	}
	return fmt.Sprintf("SCIP index not found — run 'ckb index' (uses %s)", indexer.Cmd)
}

// getSCIPInstallSuggestions returns suggestions for installing SCIP.
func (e *Engine) getSCIPInstallSuggestions() []FixAction {
	suggestions := []FixAction{
		{
			Type:        "run-command",
			Command:     "ckb index",
			Safe:        true,
			Description: "Generate SCIP index (auto-detects language)",
		},
	}

	// Detect language and suggest appropriate indexer install
	if e.hasFile("package.json") || e.hasFile("tsconfig.json") {
		suggestions = append(suggestions, FixAction{
			Type:        "run-command",
			Command:     "npm install -g @sourcegraph/scip-typescript && scip-typescript index",
			Safe:        true,
			Description: "Install and run scip-typescript",
		})
	}

	if e.hasFile("go.mod") {
		suggestions = append(suggestions, FixAction{
			Type:        "run-command",
			Command:     "go install github.com/sourcegraph/scip-go/cmd/scip-go@latest && scip-go",
			Safe:        true,
			Description: "Install and run scip-go",
		})
	}

	if e.hasFile("Cargo.toml") {
		suggestions = append(suggestions, FixAction{
			Type:        "run-command",
			Command:     "cargo install scip-rust && scip-rust",
			Safe:        true,
			Description: "Install and run scip-rust",
		})
	}

	return suggestions
}

// detectSCIPCommand detects the appropriate SCIP command for the repo.
func (e *Engine) detectSCIPCommand() string {
	if e.hasFile("package.json") || e.hasFile("tsconfig.json") {
		return "scip-typescript index"
	}
	if e.hasFile("go.mod") {
		return "scip-go"
	}
	if e.hasFile("Cargo.toml") {
		return "scip-rust"
	}
	return "scip-<language> index"
}

// hasFile checks if a file exists in the repo root.
func (e *Engine) hasFile(name string) bool {
	_, err := os.Stat(filepath.Join(e.repoRoot, name))
	return err == nil
}

// checkOrphanedIndexes scans for indexes pointing to repos that no longer exist.
func (e *Engine) checkOrphanedIndexes(ctx context.Context) DoctorCheck {
	check := DoctorCheck{
		Name: "orphaned-indexes",
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		check.Status = "info"
		check.Message = "Could not determine home directory"
		return check
	}

	cacheDir := filepath.Join(homeDir, ".ckb", "cache", "indexes")
	if _, statErr := os.Stat(cacheDir); os.IsNotExist(statErr) {
		check.Status = "pass"
		check.Message = "No index cache directory"
		return check
	}

	// Scan for index directories
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		check.Status = "info"
		check.Message = fmt.Sprintf("Could not read cache directory: %v", err)
		return check
	}

	var totalSize int64
	var repoCount int
	var orphaned []string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		indexDir := filepath.Join(cacheDir, entry.Name())
		repoCount++

		// Calculate size
		dirSize, _ := getDirSize(indexDir)
		totalSize += dirSize

		// Check if the meta.json points to a valid repo
		metaPath := filepath.Join(indexDir, "meta.json")
		if _, err := os.Stat(metaPath); os.IsNotExist(err) {
			continue // No metadata, skip
		}

		// Read meta.json to find repo path
		metaData, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		// Parse JSON metadata
		var meta indexMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			continue
		}

		if meta.RepoPath != "" {
			if _, err := os.Stat(meta.RepoPath); os.IsNotExist(err) {
				orphaned = append(orphaned, fmt.Sprintf("%s → %s", entry.Name(), meta.RepoPath))
			}
		}
	}

	if len(orphaned) > 0 {
		check.Status = "warn"
		check.Message = fmt.Sprintf("Index cache: %s (%d repos), %d orphaned (repos deleted)",
			formatBytes(totalSize), repoCount, len(orphaned))
		check.SuggestedFixes = []FixAction{
			{
				Type:        "run-command",
				Command:     "ckb cache clean --orphaned",
				Safe:        true,
				Description: fmt.Sprintf("Remove %d orphaned indexes", len(orphaned)),
			},
		}
	} else {
		check.Status = "pass"
		check.Message = fmt.Sprintf("Index cache: %s (%d repos), no orphans",
			formatBytes(totalSize), repoCount)
	}

	return check
}

// getDirSize calculates the total size of a directory.
func getDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// formatBytes formats bytes in human-readable form.
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// indexMeta represents the metadata stored with an index.
type indexMeta struct {
	RepoPath string `json:"repoPath"`
}

// checkOptionalTools verifies optional tools that enhance CKB functionality.
func (e *Engine) checkOptionalTools(ctx context.Context) DoctorCheck {
	check := DoctorCheck{
		Name: "optional-tools",
	}

	var available, missing []string
	var fixes []FixAction

	// Check gh CLI (for GitHub integration)
	if ghPath, err := exec.LookPath("gh"); err == nil {
		// Get version
		cmd := exec.Command(ghPath, "--version") // #nosec G204 //nolint:gosec // ghPath from exec.LookPath
		if output, err := cmd.Output(); err == nil {
			lines := strings.Split(string(output), "\n")
			if len(lines) > 0 {
				available = append(available, fmt.Sprintf("gh (%s)", strings.TrimPrefix(strings.Fields(lines[0])[2], "v")))
			} else {
				available = append(available, "gh")
			}
		} else {
			available = append(available, "gh")
		}
	} else {
		missing = append(missing, "gh")
		fixes = append(fixes, FixAction{
			Type:        "run-command",
			Command:     "brew install gh",
			Safe:        true,
			Description: "Install GitHub CLI for PR analysis and reviewer suggestions",
		})
	}

	// Check git version
	if gitPath, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command(gitPath, "--version") // #nosec G204 //nolint:gosec // gitPath from exec.LookPath
		if output, err := cmd.Output(); err == nil {
			version := strings.TrimPrefix(strings.TrimSpace(string(output)), "git version ")
			available = append(available, fmt.Sprintf("git (%s)", version))
		}
	}

	// Check go-diff (bundled, always available)
	available = append(available, "go-diff (bundled)")

	// Check index cache directory
	homeDir, err := os.UserHomeDir()
	if err == nil {
		cacheDir := filepath.Join(homeDir, ".ckb", "cache")
		if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
			// Try to create it
			if err := os.MkdirAll(cacheDir, 0755); err != nil {
				missing = append(missing, "cache directory")
				fixes = append(fixes, FixAction{
					Type:        "run-command",
					Command:     fmt.Sprintf("mkdir -p %s", cacheDir),
					Safe:        true,
					Description: "Create CKB cache directory",
				})
			}
		}
	}

	// Build result
	if len(missing) == 0 {
		check.Status = "pass"
		check.Message = fmt.Sprintf("Optional tools: %s", strings.Join(available, ", "))
	} else {
		check.Status = "info"
		check.Message = fmt.Sprintf("Available: %s | Missing (optional): %s",
			strings.Join(available, ", "),
			strings.Join(missing, ", "))
		check.SuggestedFixes = fixes
	}

	return check
}

// checkLIP checks whether the LIP semantic-search daemon is running and indexed.
func (e *Engine) checkLIP(_ context.Context) DoctorCheck {
	check := DoctorCheck{Name: "lip"}

	status, err := lip.IndexStatus()
	if err != nil || status == nil {
		check.Status = "warn"
		check.Message = "LIP daemon not running — semantic search and re-ranking disabled"
		check.SuggestedFixes = []FixAction{
			{
				Type:        "open-docs",
				Description: "Start the LIP daemon to enable semantic search",
			},
		}
		return check
	}

	if status.IndexedFiles == 0 {
		check.Status = "warn"
		check.Message = "LIP daemon running but no files indexed yet"
		return check
	}

	pending := ""
	if status.Pending > 0 {
		pending = fmt.Sprintf(", %d pending", status.Pending)
	}

	// Embedding coverage and staleness (best-effort; skipped if LIP has no embeddings).
	coverageMsg := ""
	if cov, _ := lip.Coverage(e.repoRoot); cov != nil && cov.TotalFiles > 0 {
		pct := int(cov.CoverageFraction * 100)
		coverageMsg = fmt.Sprintf("; %d%% embedded (%d/%d files)", pct, cov.EmbeddedFiles, cov.TotalFiles)
	}

	staleMsg := ""
	if stale, _ := lip.StaleEmbeddings(e.repoRoot); len(stale) > 0 {
		staleMsg = fmt.Sprintf("; %d stale embeddings", len(stale))
	}

	mixedMsg := ""
	if status.MixedModels {
		mixedMsg = fmt.Sprintf(" ⚠ mixed models: %s", strings.Join(status.ModelsInIndex, ", "))
	}

	check.Status = "pass"
	check.Message = fmt.Sprintf("LIP daemon running — %d files indexed%s%s%s%s",
		status.IndexedFiles, pending, coverageMsg, staleMsg, mixedMsg)
	return check
}

// countDoctorSourceFiles counts source files for the given language, used by
// the doctor SCIP check to distinguish "not indexed yet" from "deliberately skipped".
func countDoctorSourceFiles(root string, lang project.Language) int {
	var extensions []string
	switch lang {
	case project.LangGo:
		extensions = []string{".go"}
	case project.LangTypeScript, project.LangJavaScript:
		extensions = []string{".ts", ".tsx", ".js", ".jsx"}
	case project.LangPython:
		extensions = []string{".py"}
	case project.LangRust:
		extensions = []string{".rs"}
	case project.LangJava:
		extensions = []string{".java"}
	case project.LangKotlin:
		extensions = []string{".kt", ".kts"}
	case project.LangCpp:
		extensions = []string{".cpp", ".cc", ".cxx", ".c", ".h", ".hpp"}
	default:
		return 0
	}

	extSet := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		extSet[ext] = true
	}

	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".ckb", "node_modules", "vendor", ".venv", "__pycache__", "target", "build", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if extSet[filepath.Ext(path)] {
			count++
		}
		return nil
	})
	return count
}

// GenerateFixScript generates a shell script for all suggested fixes.
func (e *Engine) GenerateFixScript(response *DoctorResponse) string {
	var script strings.Builder
	script.WriteString("#!/bin/bash\n")
	script.WriteString("# CKB Doctor Fix Script\n")
	script.WriteString("# Generated at: " + time.Now().Format(time.RFC3339) + "\n")
	script.WriteString("set -e\n\n")

	for _, check := range response.Checks {
		if check.Status != "pass" && len(check.SuggestedFixes) > 0 {
			script.WriteString(fmt.Sprintf("# Fix: %s\n", check.Name))
			script.WriteString(fmt.Sprintf("# Problem: %s\n", check.Message))
			for _, fix := range check.SuggestedFixes {
				if fix.Type == "run-command" && fix.Safe {
					script.WriteString(fmt.Sprintf("echo 'Running: %s'\n", fix.Description))
					script.WriteString(fix.Command + "\n\n")
				}
			}
		}
	}

	script.WriteString("echo 'Fix script complete. Run ckb doctor to verify.'\n")
	return script.String()
}
