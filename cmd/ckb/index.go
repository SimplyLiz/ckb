package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/incremental"
	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/project"
	"github.com/SimplyLiz/CodeMCP/internal/repostate"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/tier"
)

// scipLargeRepoThreshold is the source file count above which automatic SCIP
// index generation is skipped. Above this size indexers typically take > 30 min
// and CKB falls back to FTS + LSP + LIP for search. Use --scip to override.
const scipLargeRepoThreshold = 50_000

var (
	indexForce         bool
	indexDryRun        bool
	indexLang          string
	indexCompdb        string        // Path to compile_commands.json for C/C++
	indexTier          string        // Tier to validate (enhanced, full)
	indexAllowFb       bool          // Allow fallback if tier not satisfied
	indexShowTier      bool          // Show tier summary after indexing
	indexWatch         bool          // Watch for changes and auto-reindex
	indexWatchInterval time.Duration // Watch mode polling interval
	indexSCIP          bool          // Force SCIP generation even for large repos
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Build SCIP index for full code intelligence",
	Long: `Auto-detects project language and runs the appropriate SCIP indexer.

This command enables enhanced code intelligence features like findReferences,
getCallGraph, and analyzeImpact.

For repos with more than 50,000 source files, SCIP generation is skipped
automatically — indexers can take over an hour at that scale. CKB uses
FTS + LSP + LIP semantic search instead, which covers most queries.
Run with --scip to generate the SCIP index anyway.

Supported languages:
  - Go (scip-go)
  - TypeScript/JavaScript (scip-typescript)
  - Python (scip-python)
  - Rust (rust-analyzer)
  - Java (scip-java)
  - C/C++ (scip-clang) - requires compile_commands.json
  - Dart (scip_dart)
  - Ruby (scip-ruby)
  - C# (scip-dotnet) - requires .NET 8+
  - PHP (scip-php) - requires PHP 8.2+, composer install

For Kotlin: use scip-java with Gradle plugin integration.
  - Gradle Kotlin: supported
  - Maven Kotlin: auto-config NOT supported
  - Gradle Android: NOT supported yet
See: https://sourcegraph.github.io/scip-java/

Examples:
  ckb index              # Auto-detect language and index
  ckb index --dry-run    # Show what would be run without executing
  ckb index --force      # Re-index even if index.scip exists
  ckb index --lang go    # Force specific language
  ckb index --lang cpp --compdb build/compile_commands.json`,
	Run: runIndex,
}

func init() {
	indexCmd.Flags().BoolVar(&indexForce, "force", false, "Re-index even if index.scip exists")
	indexCmd.Flags().BoolVar(&indexDryRun, "dry-run", false, "Show what would be run without executing")
	indexCmd.Flags().StringVar(&indexLang, "lang", "", "Force specific language (go, ts, py, rs, java, cpp, dart, rb, cs, php)")
	indexCmd.Flags().StringVar(&indexCompdb, "compdb", "", "Path to compile_commands.json (C/C++ only)")
	indexCmd.Flags().StringVar(&indexTier, "tier", "", "Validate tier requirements before indexing (enhanced, full)")
	indexCmd.Flags().BoolVar(&indexAllowFb, "allow-fallback", true, "Continue if tier requirements not met (default: true)")
	indexCmd.Flags().BoolVar(&indexShowTier, "show-tier", true, "Show tier summary after indexing (default: true)")
	indexCmd.Flags().BoolVar(&indexWatch, "watch", false, "Watch for changes and auto-reindex")
	indexCmd.Flags().DurationVar(&indexWatchInterval, "watch-interval", 30*time.Second,
		"Watch mode polling interval (min 5s, max 5m)")
	indexCmd.Flags().BoolVar(&indexSCIP, "scip", false,
		fmt.Sprintf("Generate SCIP index even if repo exceeds the %d-file threshold (may take > 30 min)", scipLargeRepoThreshold))
	rootCmd.AddCommand(indexCmd)
}

// indexOutcome classifies how performIndex finished, so callers (the `ckb
// index` CLI command and `ckb setup`'s auto-index step) can decide whether
// to treat it as fatal.
type indexOutcome int

const (
	// indexOutcomeIndexed means a fresh (or incremental) SCIP index was generated.
	indexOutcomeIndexed indexOutcome = iota
	// indexOutcomeUpToDate means an existing index was already fresh; nothing to do.
	indexOutcomeUpToDate
	// indexOutcomeSkippedLargeRepo means the repo exceeded scipLargeRepoThreshold
	// and SCIP generation was skipped in favor of FTS + LSP + LIP.
	indexOutcomeSkippedLargeRepo
	// indexOutcomeNoLanguageDetected means no supported manifest was found.
	indexOutcomeNoLanguageDetected
	// indexOutcomeMultipleLanguages means more than one language was detected
	// and the caller must disambiguate with --lang.
	indexOutcomeMultipleLanguages
	// indexOutcomeIndexerMissing means the language is supported but its SCIP
	// indexer binary isn't installed (or no indexer exists for the language
	// at all). This is recoverable: Git-based features still work.
	indexOutcomeIndexerMissing
	// indexOutcomeIndexerRequirementMissing means the indexer is installed but
	// a prerequisite is missing (compile_commands.json, bundler, composer, ...).
	indexOutcomeIndexerRequirementMissing
	// indexOutcomeIndexingFailed means the indexer ran but exited non-zero or
	// didn't produce an index file.
	indexOutcomeIndexingFailed
	// indexOutcomeError means something unrelated to indexer availability
	// failed (e.g. .ckb missing, filesystem error, lock contention).
	indexOutcomeError
)

// indexResult is the outcome of performIndex. Message is a short, already
// human-readable summary suitable for a caller (like `ckb setup`) that wants
// to report the situation in one line without needing to know the internals.
// performIndex itself always prints full detail to stdout/stderr as it goes.
type indexResult struct {
	Outcome indexOutcome
	Message string
	Err     error
	Lang    project.Language
	// Warning is a non-fatal, cosmetic note (currently only PHP's missing
	// composer.lock notice) that a caller may want to print regardless of
	// whether plan-building succeeded or failed.
	Warning string
}

// indexPlan is the fully-resolved recipe for building a SCIP index for one
// language: the exact command to run, the directory to run it from, and the
// resolved index output path. performIndex (`ckb index`) and triggerReindex
// (the `ckb mcp --watch` background loop) both build their command through
// buildIndexPlan instead of each hand-rolling it — a C++ --compdb-path flag,
// a Ruby bundle-aware prefix, a PHP prerequisite check, or a custom
// scip.indexPath --output flag added on one path and not the other is
// exactly the drift that made watch mode silently fail to build a usable
// index for C++ repos and custom output paths.
type indexPlan struct {
	Lang       project.Language
	Manifest   string
	Indexer    *project.IndexerInfo
	Command    string
	IndexerDir string
	IndexPath  string
}

// resolveIndexPath resolves the SCIP index output path for repoRoot: the
// configured scip.indexPath if set, else the default index.scip at the repo
// root. Always returns an absolute path. Both performIndex and
// buildIndexPlan (and therefore triggerReindex) call this — a divergence
// here is exactly what let the watch loop silently ignore a custom
// scip.indexPath: it never added --output, and never checked the right file
// for existence afterward.
func resolveIndexPath(repoRoot string) string {
	indexPath := "index.scip"
	if cfg, err := config.LoadConfig(repoRoot); err == nil && cfg.Backends.Scip.IndexPath != "" {
		indexPath = cfg.Backends.Scip.IndexPath
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(repoRoot, indexPath)
	}
	return indexPath
}

// resolveIndexLanguage resolves which language to index for repoRoot: either
// langOverride (from --lang, when not project.LangUnknown) or auto-detection
// via project.DetectAllLanguages. ok is false when detection failed — either
// no language was found (allLangs is nil) or more than one was found without
// an explicit override (allLangs lists them, for the caller to print).
func resolveIndexLanguage(repoRoot string, langOverride project.Language) (lang project.Language, manifest string, allLangs []project.Language, ok bool) {
	if langOverride != project.LangUnknown {
		lang = langOverride
		manifest = project.FindManifestForLanguage(repoRoot, lang)
		if manifest == "" {
			manifest = "(specified via --lang)"
		}
		return lang, manifest, nil, true
	}

	lang, manifest, allLangs = project.DetectAllLanguages(repoRoot)
	if lang == project.LangUnknown {
		return project.LangUnknown, "", nil, false
	}
	if len(allLangs) > 1 {
		return lang, manifest, allLangs, false
	}
	return lang, manifest, nil, true
}

// buildIndexPlan builds the indexer command for lang/manifest given the
// resolved index output path (see resolveIndexPath) and an optional C/C++
// compdb override (empty string means "auto-detect"). Returns a nil plan and
// a failure indexResult (indexOutcomeIndexerMissing,
// indexOutcomeIndexerRequirementMissing, or indexOutcomeError) when the plan
// can't be built — the caller must not proceed to execution in that case.
// The returned indexResult's Warning field may be set even when plan is
// non-nil (currently only PHP's missing composer.lock notice), so callers
// should check it independently of success/failure.
func buildIndexPlan(repoRoot string, lang project.Language, manifest, compdbOverride, indexPath string) (*indexPlan, indexResult) {
	indexer := project.GetIndexerInfo(lang)
	if indexer == nil {
		return nil, indexResult{
			Outcome: indexOutcomeIndexerMissing,
			Message: fmt.Sprintf("no SCIP indexer available for %s", project.LanguageDisplayName(lang)),
			Lang:    lang,
		}
	}

	// Check if using non-default index path (requires --output flag)
	defaultIndexPath := filepath.Join(repoRoot, "index.scip")
	needsOutputFlag := indexPath != defaultIndexPath

	// Ensure output directory exists (for custom paths)
	if needsOutputFlag {
		outputDir := filepath.Dir(indexPath)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return nil, indexResult{Outcome: indexOutcomeError, Message: fmt.Sprintf("could not create index directory: %v", err), Err: err, Lang: lang}
		}
	}

	var command string
	var warning string

	// Build command - some languages need special handling
	switch lang {
	case project.LangCpp:
		cppCmd, err := project.BuildCppCommand(repoRoot, compdbOverride)
		if err != nil || cppCmd == "" {
			return nil, indexResult{
				Outcome: indexOutcomeIndexerRequirementMissing,
				Message: "compile_commands.json not found (generate with cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON -B build)",
				Lang:    lang,
			}
		}
		command = cppCmd

	case project.LangRuby:
		rubyCmd, err := project.BuildRubyCommand(repoRoot)
		if err != nil {
			return nil, indexResult{
				Outcome: indexOutcomeIndexerRequirementMissing,
				Message: "bundle not found (install with: gem install bundler)",
				Lang:    lang,
			}
		}
		command = rubyCmd

	case project.LangPHP:
		w, err := project.ValidatePHPSetup(repoRoot)
		warning = w
		if err != nil {
			return nil, indexResult{
				Outcome: indexOutcomeIndexerRequirementMissing,
				Message: "scip-php not installed (composer require --dev davidrjenni/scip-php && composer install)",
				Lang:    lang,
				Warning: warning,
			}
		}
		command = indexer.Command

	default:
		// Standard languages: use base command as-is.
		command = indexer.Command
	}

	if needsOutputFlag {
		command = fmt.Sprintf("%s --output %s", command, indexPath)
	}

	// Check if indexer is installed
	if !isIndexerInstalled(indexer.CheckCommand) {
		return nil, indexResult{
			Outcome: indexOutcomeIndexerMissing,
			Message: fmt.Sprintf("%s not installed (install with: %s)", indexer.CheckCommand, indexer.InstallCommand),
			Lang:    lang,
			Warning: warning,
		}
	}

	// Run the indexer from the manifest's directory.
	// For monorepos, the manifest may be in a subdirectory (e.g., src/cli/go.mod).
	indexerDir := repoRoot
	if manifest != "" && manifest != "(specified via --lang)" {
		manifestDir := filepath.Dir(filepath.Join(repoRoot, manifest))
		if manifestDir != repoRoot {
			indexerDir = manifestDir
		}
	}

	return &indexPlan{
		Lang:       lang,
		Manifest:   manifest,
		Indexer:    indexer,
		Command:    command,
		IndexerDir: indexerDir,
		IndexPath:  indexPath,
	}, indexResult{Warning: warning}
}

// printIndexPlanFailure prints the same stderr diagnostics performIndex has
// always printed for each buildIndexPlan failure outcome, now driven off the
// returned indexResult instead of being inlined at each return site.
func printIndexPlanFailure(lang project.Language, result indexResult) {
	switch result.Outcome {
	case indexOutcomeIndexerMissing:
		indexer := project.GetIndexerInfo(lang)
		if indexer == nil {
			fmt.Fprintf(os.Stderr, "No SCIP indexer available for %s\n", project.LanguageDisplayName(lang))
			return
		}
		fmt.Println()
		fmt.Printf("Indexer not found: %s\n", indexer.CheckCommand)
		fmt.Println()
		fmt.Println("Install with:")
		fmt.Printf("  %s\n", indexer.InstallCommand)

	case indexOutcomeIndexerRequirementMissing:
		switch lang {
		case project.LangCpp:
			fmt.Fprintln(os.Stderr, "compile_commands.json not found.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Generate it with CMake:")
			fmt.Fprintln(os.Stderr, "  cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON -B build")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Or specify path:")
			fmt.Fprintln(os.Stderr, "  ckb index --lang cpp --compdb build/compile_commands.json")
		case project.LangRuby:
			fmt.Fprintln(os.Stderr, "bundle not found. Install Bundler:")
			fmt.Fprintln(os.Stderr, "  gem install bundler")
		case project.LangPHP:
			fmt.Fprintln(os.Stderr, "scip-php not installed.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Install with:")
			fmt.Fprintln(os.Stderr, "  composer require --dev davidrjenni/scip-php")
			fmt.Fprintln(os.Stderr, "  composer install")
		}

	case indexOutcomeError:
		fmt.Fprintf(os.Stderr, "Error creating index directory: %v\n", result.Err)
	}
}

func runIndex(cmd *cobra.Command, args []string) {
	repoRoot := mustGetRepoRoot()

	result := performIndex(repoRoot)

	switch result.Outcome {
	case indexOutcomeIndexed, indexOutcomeUpToDate, indexOutcomeSkippedLargeRepo:
		// Success or benign no-op.
	default:
		os.Exit(1)
	}

	// Start watch mode if enabled (CLI-only; ckb setup never sets this).
	if indexWatch && result.Outcome == indexOutcomeIndexed {
		fmt.Println()
		ckbDir := filepath.Join(repoRoot, ".ckb")
		runIndexWatchLoop(repoRoot, ckbDir, result.Lang)
	}
}

// performIndex contains the actual indexing logic shared by `ckb index` and
// `ckb setup`'s auto-index step. Unlike the old runIndex, it never calls
// os.Exit — every outcome (success, benign skip, or failure) is returned so
// callers can decide how to react. `ckb setup` in particular must not die
// just because an indexer isn't installed; it prints one line and moves on.
func performIndex(repoRoot string) indexResult {
	// Check if this is an initialized CKB project
	ckbDir := filepath.Join(repoRoot, ".ckb")
	if _, err := os.Stat(ckbDir); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Error: Not a CKB project.")
		fmt.Fprintln(os.Stderr, "Run 'ckb init' first to initialize this directory.")
		return indexResult{Outcome: indexOutcomeError, Message: "not a CKB project (run 'ckb init' first)"}
	}

	// Resolve SCIP index output path (shared with buildIndexPlan/triggerReindex
	// so a custom scip.indexPath is never silently ignored by one caller).
	indexPath := resolveIndexPath(repoRoot)

	// Migration: move legacy .scip/index.scip to root if needed
	migrateIndexPath(repoRoot, indexPath)

	// Check index freshness (unless --force)
	if !indexForce {
		meta, err := index.LoadMeta(ckbDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not load index metadata: %v\n", err)
		}

		if meta != nil {
			freshness := meta.CheckFreshness(repoRoot)
			if freshness.Fresh {
				// Show brief info about current index
				if info, err := os.Stat(indexPath); err == nil {
					commitInfo := ""
					if freshness.IndexedCommit != "" {
						commitInfo = fmt.Sprintf(" (HEAD = %s)", shortHash(freshness.IndexedCommit))
					}
					fmt.Printf("Index is current%s\n", commitInfo)
					fmt.Printf("  %d files, %.2f MB\n", meta.FileCount, float64(info.Size())/1024/1024)
					fmt.Println("Nothing to do. Use --force to re-index.")
					return indexResult{Outcome: indexOutcomeUpToDate, Message: "index is current"}
				}
			} else {
				// Show why index is stale
				fmt.Printf("Index is stale: %s\n", freshness.Reason)
			}
		} else {
			// No metadata but index.scip exists - legacy index
			if info, err := os.Stat(indexPath); err == nil {
				fmt.Printf("Found legacy index: %s (%.2f MB)\n", indexPath, float64(info.Size())/1024/1024)
				fmt.Println("Re-indexing to enable freshness tracking...")
			}
		}
	}

	// Detect or use specified language. Shared with triggerReindex through
	// buildIndexPlan below (triggerReindex resolves its language separately
	// via watchDetectLanguage, since it prefers the previously-saved project
	// language over re-detecting on every tick, then calls buildIndexPlan
	// with the result — same as this function does).
	langOverride := project.LangUnknown
	if indexLang != "" {
		langOverride = parseLanguageFlag(indexLang)
		if langOverride == project.LangUnknown {
			fmt.Fprintf(os.Stderr, "Unsupported language: %s\n", indexLang)
			fmt.Fprintln(os.Stderr, "Supported: go, ts, py, rs, java, cpp, dart, rb, cs, php")
			return indexResult{Outcome: indexOutcomeError, Message: fmt.Sprintf("unsupported language: %s", indexLang)}
		}
	}

	lang, manifest, allLangs, ok := resolveIndexLanguage(repoRoot, langOverride)
	if !ok {
		if len(allLangs) > 1 {
			fmt.Fprintln(os.Stderr, "Multiple languages detected:")
			for _, l := range allLangs {
				fmt.Fprintf(os.Stderr, "  - %s\n", project.LanguageDisplayName(l))
			}
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Use --lang to specify which language to index:")
			fmt.Fprintf(os.Stderr, "  ckb index --lang %s\n", allLangs[0])
			return indexResult{Outcome: indexOutcomeMultipleLanguages, Message: "multiple languages detected — rerun with --lang"}
		}
		fmt.Fprintln(os.Stderr, "Could not detect project language.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Supported manifest files:")
		fmt.Fprintln(os.Stderr, "  Go:         go.mod")
		fmt.Fprintln(os.Stderr, "  TypeScript: package.json + tsconfig.json")
		fmt.Fprintln(os.Stderr, "  Python:     pyproject.toml, requirements.txt, setup.py")
		fmt.Fprintln(os.Stderr, "  Rust:       Cargo.toml")
		fmt.Fprintln(os.Stderr, "  Java:       pom.xml, build.gradle")
		fmt.Fprintln(os.Stderr, "  Kotlin:     build.gradle.kts")
		fmt.Fprintln(os.Stderr, "  C/C++:      compile_commands.json")
		fmt.Fprintln(os.Stderr, "  Dart:       pubspec.yaml")
		fmt.Fprintln(os.Stderr, "  Ruby:       Gemfile, *.gemspec")
		fmt.Fprintln(os.Stderr, "  C#:         *.csproj, *.sln")
		fmt.Fprintln(os.Stderr, "  PHP:        composer.json")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Or specify manually: ckb index --lang go")
		return indexResult{Outcome: indexOutcomeNoLanguageDetected, Message: "could not detect project language"}
	}

	fmt.Printf("Detected %s project (from %s)\n", project.LanguageDisplayName(lang), manifest)

	// Large-repo gate: count source files and skip SCIP if above threshold.
	// SCIP indexers take 30–90 min on large monorepos; CKB falls back to
	// FTS + LSP + LIP which handles most queries without the index.
	fileCount := countSourceFiles(repoRoot, lang)
	if fileCount >= scipLargeRepoThreshold {
		if indexSCIP || indexForce {
			fmt.Printf("Warning: %d source files detected — SCIP generation may take 30–90 min.\n", fileCount)
			fmt.Println("         Proceeding because --scip / --force was specified.")
			fmt.Println()
		} else {
			printLargeRepoNotice(lang, fileCount, indexPath)
			return indexResult{Outcome: indexOutcomeSkippedLargeRepo, Message: fmt.Sprintf("repo too large for automatic SCIP indexing (%d files)", fileCount), Lang: lang}
		}
	}

	// Build the indexer command — shared with triggerReindex so a C++ compdb
	// flag, Ruby bundle-aware prefix, PHP prerequisite check, or custom
	// scip.indexPath --output flag can never drift between the CLI path and
	// the watch loop's background reindex.
	plan, planResult := buildIndexPlan(repoRoot, lang, manifest, indexCompdb, indexPath)
	if planResult.Warning != "" {
		fmt.Printf("Warning: %s\n", planResult.Warning)
	}
	if plan == nil {
		printIndexPlanFailure(lang, planResult)
		return planResult
	}

	fmt.Printf("Indexer: %s\n", plan.Indexer.CheckCommand)
	fmt.Printf("Command: %s\n", plan.Command)

	// Dry run - show command without executing
	if indexDryRun {
		fmt.Println()
		fmt.Println("[dry-run] Would execute the above command")
		return indexResult{Outcome: indexOutcomeUpToDate, Message: "dry run", Lang: lang}
	}

	// Try incremental indexing for supported languages (unless --force)
	if !indexForce && project.SupportsIncrementalIndexing(lang) {
		if tryIncrementalIndex(repoRoot, ckbDir, lang) {
			// Incremental succeeded, we're done
			return indexResult{Outcome: indexOutcomeIndexed, Lang: lang}
		}
		// Fall through to full index
	}

	// Acquire lock to prevent concurrent indexing
	lock, err := index.AcquireLock(ckbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return indexResult{Outcome: indexOutcomeError, Message: fmt.Sprintf("could not acquire index lock: %v", err), Err: err, Lang: lang}
	}
	defer lock.Release()

	// Run the indexer from the manifest's directory.
	// For monorepos, the manifest may be in a subdirectory (e.g., src/cli/go.mod).
	if plan.IndexerDir != repoRoot {
		fmt.Printf("Module root: %s\n", plan.Manifest)
	}

	fmt.Println()
	fmt.Println("Generating SCIP index...")
	fmt.Println()

	start := time.Now()
	err = runIndexerCommand(plan.IndexerDir, plan.Command)
	duration := time.Since(start)

	if err != nil {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Indexing failed.")
		fmt.Fprintln(os.Stderr, "")
		showTroubleshooting(lang)
		return indexResult{Outcome: indexOutcomeIndexingFailed, Message: "indexer failed", Err: err, Lang: lang}
	}

	// Verify index was created
	info, err := os.Stat(plan.IndexPath)
	if os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Warning: Indexer completed but index.scip was not created.")
		fmt.Fprintln(os.Stderr, "Check the indexer output above for errors.")
		return indexResult{Outcome: indexOutcomeIndexingFailed, Message: "indexer completed but produced no index.scip", Lang: lang}
	}

	// Save project config
	config := &project.ProjectConfig{
		Language:     lang,
		Indexer:      plan.Indexer.CheckCommand,
		ManifestPath: manifest,
		DetectedAt:   time.Now(),
	}
	if saveErr := project.SaveConfig(repoRoot, config); saveErr != nil {
		// Non-fatal, just warn
		fmt.Fprintf(os.Stderr, "Warning: Could not save project config: %v\n", saveErr)
	}

	// Save index metadata for freshness tracking
	meta := &index.IndexMeta{
		CreatedAt:   time.Now(),
		FileCount:   countSourceFiles(repoRoot, lang),
		Duration:    duration.Round(time.Millisecond * 100).String(),
		Indexer:     plan.Indexer.CheckCommand,
		IndexerArgs: strings.Fields(plan.Command),
	}

	// Capture git state if available
	if rs, rsErr := repostate.ComputeRepoState(repoRoot); rsErr == nil {
		meta.CommitHash = rs.HeadCommit
		meta.RepoStateID = rs.RepoStateID
	}

	if saveErr := meta.Save(ckbDir); saveErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not save index metadata: %v\n", saveErr)
	}

	// Populate incremental tracking tables for supported languages
	if project.SupportsIncrementalIndexing(lang) {
		populateIncrementalTracking(repoRoot, lang)
	}

	// Success message
	fmt.Println()
	fmt.Printf("Done! Indexed in %.1fs\n", duration.Seconds())
	fmt.Printf("Index: %s (%.2f MB)\n", indexPath, float64(info.Size())/1024/1024)
	fmt.Println()
	fmt.Println("Full code intelligence now available:")
	fmt.Println("  findReferences - Find all usages of a symbol")
	fmt.Println("  getCallGraph   - Trace caller/callee relationships")
	fmt.Println("  analyzeImpact  - Assess change impact")
	fmt.Println()

	// Show tier summary if enabled
	if indexShowTier {
		showTierSummary(repoRoot, lang)
	}

	fmt.Println("Run 'ckb status' to verify.")

	return indexResult{Outcome: indexOutcomeIndexed, Lang: lang}
}

// showTierSummary displays the current tier status after indexing.
func showTierSummary(repoRoot string, lang project.Language) {
	// Convert project.Language to tier.Language
	tierLang, ok := tier.ParseLanguage(string(lang))
	if !ok {
		return
	}

	ctx := context.Background()
	runner := tier.NewCachingRunner(tier.NewRealRunner(5 * time.Second))
	detector := tier.NewToolDetector(runner, 5*time.Second)

	status := detector.DetectLanguageTier(ctx, tierLang)

	fmt.Println("Tier Status:")
	fmt.Printf("  %s: %s tier\n", status.DisplayName, tierDisplayNameShort(status.ToolTier))

	// Show available capabilities
	if len(status.Capabilities) > 0 {
		fmt.Print("  Capabilities: ")
		caps := []string{}
		for cap, enabled := range status.Capabilities {
			if enabled {
				caps = append(caps, cap)
			}
		}
		fmt.Println(strings.Join(caps, ", "))
	}

	// Show upgrade hint if not at full tier
	if status.ToolTier < tier.TierFull {
		switch status.ToolTier {
		case tier.TierBasic:
			fmt.Println("  Tip: Run 'ckb doctor --tier enhanced' to see what's needed for more features.")
		case tier.TierEnhanced:
			fmt.Println("  Tip: Run 'ckb doctor --tier full' to see what's needed for LSP features.")
		}
	}
	fmt.Println()
}

// tierDisplayNameShort returns a short tier name.
func tierDisplayNameShort(t tier.AnalysisTier) string {
	switch t {
	case tier.TierBasic:
		return "basic"
	case tier.TierEnhanced:
		return "enhanced"
	case tier.TierFull:
		return "full"
	default:
		return "unknown"
	}
}

// parseLanguageFlag converts a language flag to a Language type.
func parseLanguageFlag(flag string) project.Language {
	flag = strings.ToLower(flag)
	switch flag {
	case "go", "golang":
		return project.LangGo
	case "ts", "typescript":
		return project.LangTypeScript
	case "js", "javascript":
		return project.LangJavaScript
	case "py", "python":
		return project.LangPython
	case "rs", "rust":
		return project.LangRust
	case "java":
		return project.LangJava
	case "kt", "kotlin":
		return project.LangKotlin
	case "c", "c++", "cc", "cxx", "cpp":
		return project.LangCpp
	case "dart":
		return project.LangDart
	case "rb", "ruby":
		return project.LangRuby
	case "cs", "c#", "csharp", "dotnet":
		return project.LangCSharp
	case "php":
		return project.LangPHP
	default:
		return project.LangUnknown
	}
}

// migrateIndexPath moves legacy .scip/index.scip to root if needed.
// This handles migration from older CKB versions that used .scip/ subdirectory.
func migrateIndexPath(repoRoot, targetPath string) {
	legacyPath := filepath.Join(repoRoot, ".scip", "index.scip")

	// Only migrate if legacy exists and target doesn't
	if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
		return // No legacy index
	}
	if _, err := os.Stat(targetPath); err == nil {
		return // Target already exists
	}

	// Move legacy to target
	if err := os.Rename(legacyPath, targetPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not migrate index from %s to %s: %v\n", legacyPath, targetPath, err)
		return
	}

	fmt.Printf("Migrated index: %s -> %s\n", legacyPath, targetPath)

	// Clean up empty .scip directory
	if entries, err := os.ReadDir(filepath.Join(repoRoot, ".scip")); err == nil && len(entries) == 0 {
		os.Remove(filepath.Join(repoRoot, ".scip"))
	}
}

// isIndexerInstalled checks if the indexer command is available.
func isIndexerInstalled(command string) bool {
	// Extract just the binary name (first word)
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}
	binary := parts[0]

	// Try to find in PATH
	_, err := exec.LookPath(binary)
	return err == nil
}

// runIndexerCommand runs the indexer and streams output.
// By default, indexer output is captured and only shown on error.
// With -v, output streams to stderr in real-time.
func runIndexerCommand(dir, command string) error {
	// Split command into parts
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...) // #nosec G204 //nolint:gosec // command from trusted indexer config
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer

	if verbosity > 0 {
		// Verbose mode: stream to stderr (keeps stdout clean for piping)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	} else {
		// Default: capture output, only show on error
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	err := cmd.Run()
	if err != nil {
		// Show captured output on error (if not already streamed)
		if verbosity == 0 {
			if stdout.Len() > 0 {
				fmt.Fprintln(os.Stderr, "Indexer stdout:")
				fmt.Fprintln(os.Stderr, stdout.String())
			}
			if stderr.Len() > 0 {
				fmt.Fprintln(os.Stderr, "Indexer stderr:")
				fmt.Fprintln(os.Stderr, stderr.String())
			}
		}
		return fmt.Errorf("indexer failed: %w", err)
	}

	return nil
}

// showTroubleshooting shows language-specific troubleshooting tips.
func showTroubleshooting(lang project.Language) {
	switch lang {
	case project.LangGo:
		fmt.Fprintln(os.Stderr, "Troubleshooting for Go:")
		fmt.Fprintln(os.Stderr, "  1. Ensure your code compiles: go build ./...")
		fmt.Fprintln(os.Stderr, "  2. Check for missing dependencies: go mod tidy")
		fmt.Fprintln(os.Stderr, "  3. Try updating scip-go: go install github.com/sourcegraph/scip-go@latest")

	case project.LangTypeScript, project.LangJavaScript:
		fmt.Fprintln(os.Stderr, "Troubleshooting for TypeScript/JavaScript:")
		fmt.Fprintln(os.Stderr, "  1. Ensure dependencies are installed: npm install")
		fmt.Fprintln(os.Stderr, "  2. Check for TypeScript errors: npx tsc --noEmit")
		fmt.Fprintln(os.Stderr, "  3. Ensure tsconfig.json exists for TypeScript projects")

	case project.LangPython:
		fmt.Fprintln(os.Stderr, "Troubleshooting for Python:")
		fmt.Fprintln(os.Stderr, "  1. Ensure dependencies are installed: pip install -r requirements.txt")
		fmt.Fprintln(os.Stderr, "  2. Check for syntax errors: python -m py_compile *.py")

	case project.LangRust:
		fmt.Fprintln(os.Stderr, "Troubleshooting for Rust:")
		fmt.Fprintln(os.Stderr, "  1. Ensure your code compiles: cargo build")
		fmt.Fprintln(os.Stderr, "  2. Check rust-analyzer is installed: rustup component add rust-analyzer")

	case project.LangJava, project.LangKotlin:
		fmt.Fprintln(os.Stderr, "Troubleshooting for Java/Kotlin:")
		fmt.Fprintln(os.Stderr, "  1. Ensure your code compiles with your build tool")
		fmt.Fprintln(os.Stderr, "  2. Check scip-java is properly installed via Coursier")

	case project.LangCpp:
		fmt.Fprintln(os.Stderr, "Troubleshooting for C/C++:")
		fmt.Fprintln(os.Stderr, "  1. Ensure compile_commands.json exists and is valid")
		fmt.Fprintln(os.Stderr, "  2. Run from project root, even if compdb is in build/")
		fmt.Fprintln(os.Stderr, "  3. Generate with: cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON -B build")

	case project.LangDart:
		fmt.Fprintln(os.Stderr, "Troubleshooting for Dart:")
		fmt.Fprintln(os.Stderr, "  1. Ensure dependencies are fetched: dart pub get")
		fmt.Fprintln(os.Stderr, "  2. Check scip_dart is activated: dart pub global activate scip_dart")

	case project.LangRuby:
		fmt.Fprintln(os.Stderr, "Troubleshooting for Ruby:")
		fmt.Fprintln(os.Stderr, "  1. Ensure dependencies are installed: bundle install")
		fmt.Fprintln(os.Stderr, "  2. If using Sorbet, check sorbet/config exists")
		fmt.Fprintln(os.Stderr, "  3. Check scip-ruby is installed from releases")

	case project.LangCSharp:
		fmt.Fprintln(os.Stderr, "Troubleshooting for C#:")
		fmt.Fprintln(os.Stderr, "  1. Ensure .NET 8+ is installed: dotnet --version")
		fmt.Fprintln(os.Stderr, "  2. Check scip-dotnet is on PATH: $HOME/.dotnet/tools")
		fmt.Fprintln(os.Stderr, "  3. Ensure project builds: dotnet build")

	case project.LangPHP:
		fmt.Fprintln(os.Stderr, "Troubleshooting for PHP:")
		fmt.Fprintln(os.Stderr, "  1. Ensure PHP 8.2+ is installed: php --version")
		fmt.Fprintln(os.Stderr, "  2. Run: composer install")
		fmt.Fprintln(os.Stderr, "  3. Check vendor/bin/scip-php exists")

	default:
		fmt.Fprintln(os.Stderr, "Check that the project compiles without errors first.")
	}
}

// shortHash returns the first 7 characters of a git hash.
func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// printLargeRepoNotice is printed when countSourceFiles exceeds scipLargeRepoThreshold.
// It explains what tier is active, what's missing, and how to opt in to SCIP.
func printLargeRepoNotice(lang project.Language, fileCount int, indexPath string) {
	indexer := project.GetIndexerInfo(lang)

	fmt.Println()
	fmt.Printf("⚠  Repo too large for automatic SCIP indexing (%d files, threshold %d)\n",
		fileCount, scipLargeRepoThreshold)
	fmt.Println()
	fmt.Println("SCIP generation is disabled — indexers take 30–90 min at this scale.")
	fmt.Println("CKB will use FTS + LSP + LIP semantic search instead.")
	fmt.Println()
	fmt.Println("Available without SCIP:")
	fmt.Println("  ✓  Symbol search (FTS + semantic re-ranking via LIP)")
	fmt.Println("  ✓  Go-to-definition, find references (via LSP)")
	fmt.Println("  ✓  Semantic search when symbol names don't match (via LIP nearest-by-text)")
	fmt.Println()
	fmt.Println("Requires SCIP:")
	fmt.Println("  ✗  Cross-file call graph (getCallGraph)")
	fmt.Println("  ✗  Change impact analysis (analyzeImpact)")
	fmt.Println("  ✗  Dependency tracking (getHotspots, analyzeCoupling)")
	fmt.Println()

	if indexer != nil {
		fmt.Println("To generate SCIP manually (then run ckb index --scip to register it):")
		fmt.Printf("  %s\n", indexer.Command)
		fmt.Println()
		fmt.Println("Or force automatic generation (may take a long time):")
	} else {
		fmt.Println("To force SCIP generation regardless of repo size:")
	}
	fmt.Println("  ckb index --scip")
	fmt.Println()
	fmt.Printf("Index will be written to: %s\n", indexPath)
	fmt.Println()
	fmt.Println("Run 'ckb doctor' to confirm the active tier.")
}

// countSourceFiles counts source files in the repository for the given language.
func countSourceFiles(root string, lang project.Language) int {
	extensions := getSourceExtensions(lang)
	if len(extensions) == 0 {
		return 0
	}

	extSet := make(map[string]bool)
	for _, ext := range extensions {
		extSet[ext] = true
	}

	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // Skip errors, continue walking
		}
		if d.IsDir() {
			// Skip common non-source directories
			switch d.Name() {
			case ".git", ".ckb", "node_modules", "vendor", ".venv", "__pycache__", "target", "build", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if extSet[ext] {
			count++
		}
		return nil
	})
	return count
}

// getSourceExtensions returns file extensions for the given language.
func getSourceExtensions(lang project.Language) []string {
	switch lang {
	case project.LangGo:
		return []string{".go"}
	case project.LangTypeScript:
		return []string{".ts", ".tsx"}
	case project.LangJavaScript:
		return []string{".js", ".jsx"}
	case project.LangPython:
		return []string{".py"}
	case project.LangRust:
		return []string{".rs"}
	case project.LangJava:
		return []string{".java"}
	case project.LangKotlin:
		return []string{".kt", ".kts"}
	case project.LangCpp:
		return []string{".cpp", ".cc", ".cxx", ".c", ".h", ".hpp"}
	case project.LangDart:
		return []string{".dart"}
	case project.LangRuby:
		return []string{".rb"}
	case project.LangCSharp:
		return []string{".cs"}
	case project.LangPHP:
		return []string{".php"}
	default:
		return nil
	}
}

// tryIncrementalIndex attempts incremental indexing for supported languages.
// Returns true if incremental succeeded (caller should return early).
// Returns false if full reindex is needed.
func tryIncrementalIndex(repoRoot, ckbDir string, lang project.Language) bool {
	dbPath := filepath.Join(ckbDir, "ckb.db")

	// Check if database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// No database = no previous index
		return false
	}

	// Create logger (silent for CLI)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Open database
	db, err := storage.Open(repoRoot, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not open database for incremental: %v\n", err)
		return false
	}
	defer func() { _ = db.Close() }()

	// Get SCIP index path from config (default: index.scip in root)
	indexPath := "index.scip"
	if cfg, loadErr := config.LoadConfig(repoRoot); loadErr == nil && cfg.Backends.Scip.IndexPath != "" {
		indexPath = cfg.Backends.Scip.IndexPath
	}

	// Create incremental config with the configured index path
	incConfig := &incremental.Config{
		IndexPath:            indexPath,
		IncrementalThreshold: 50,
		IndexTests:           false,
	}

	// Create incremental indexer
	indexer := incremental.NewIncrementalIndexer(repoRoot, db, incConfig, logger)

	// Check if we need full reindex
	needsFull, reason := indexer.NeedsFullReindex()
	if needsFull {
		fmt.Printf("Full reindex required: %s\n", reason)
		return false
	}

	// Acquire lock to prevent concurrent indexing
	lock, err := index.AcquireLock(ckbDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return false
	}
	defer lock.Release()

	// Check if incremental is available for this language
	canUse, reason := indexer.CanUseIncremental(lang)
	if !canUse {
		fmt.Printf("Incremental not available: %s\n", reason)
		return false
	}

	// Try incremental update
	ctx := context.Background()
	stats, err := indexer.IndexIncrementalWithLang(ctx, "", lang)
	if err != nil {
		// Check for specific errors that should fall back to full reindex
		if strings.Contains(err.Error(), "not supported") ||
			strings.Contains(err.Error(), "not installed") {
			fmt.Printf("Incremental not available: %v\n", err)
			return false
		}
		fmt.Printf("Incremental failed: %v\n", err)
		fmt.Println("Falling back to full reindex...")
		return false
	}

	// Get current state for display
	state := indexer.GetIndexState()

	// Format and display results
	fmt.Println(incremental.FormatStats(stats, state))

	// Update metadata so freshness check stays in sync with incremental state
	if rs, rsErr := repostate.ComputeRepoState(repoRoot); rsErr == nil {
		meta, metaErr := index.LoadMeta(ckbDir)
		if metaErr == nil && meta != nil {
			meta.CommitHash = rs.HeadCommit
			meta.RepoStateID = rs.RepoStateID
			if saveErr := meta.Save(ckbDir); saveErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: Could not update index metadata: %v\n", saveErr)
			}
		}
	}

	return true
}

// populateIncrementalTracking sets up tracking tables after a full index.
// This enables subsequent incremental updates.
func populateIncrementalTracking(repoRoot string, lang project.Language) {
	// Create logger (silent for CLI)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Open database
	db, err := storage.Open(repoRoot, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not open database for incremental tracking: %v\n", err)
		return
	}
	defer func() { _ = db.Close() }()

	// Get SCIP index path from config (default: index.scip in root)
	indexPath := "index.scip"
	if cfg, loadErr := config.LoadConfig(repoRoot); loadErr == nil && cfg.Backends.Scip.IndexPath != "" {
		indexPath = cfg.Backends.Scip.IndexPath
	}

	// Create incremental config with the configured index path
	incConfig := &incremental.Config{
		IndexPath:            indexPath,
		IncrementalThreshold: 50,
		IndexTests:           false,
	}

	// Create incremental indexer
	indexer := incremental.NewIncrementalIndexer(repoRoot, db, incConfig, logger)

	// Populate tracking tables from the full index
	if popErr := indexer.PopulateAfterFullIndex(); popErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not populate incremental tracking: %v\n", popErr)
		return
	}

	fmt.Println("  Incremental tracking enabled for future updates")
}

// runIndexWatchLoop watches for changes and runs incremental updates.
func runIndexWatchLoop(repoRoot, ckbDir string, lang project.Language) {
	// Validate and clamp watch interval
	interval := indexWatchInterval
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	if interval > 5*time.Minute {
		interval = 5 * time.Minute
	}

	fmt.Printf("Watching for changes... (polling every %s, Ctrl+C to stop)\n", interval)

	// Setup signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Track last known state for change detection
	lastCommit := ""
	if meta, err := index.LoadMeta(ckbDir); err == nil && meta != nil {
		lastCommit = meta.CommitHash
	}

	for {
		select {
		case <-sigCh:
			fmt.Println("\nStopping watch...")
			return

		case <-ticker.C:
			// Check if there are new commits
			currentCommit := getCurrentCommit(repoRoot)
			if currentCommit == "" || currentCommit == lastCommit {
				continue
			}

			fmt.Printf("\nChanges detected (commit %s -> %s)\n", shortHash(lastCommit), shortHash(currentCommit))

			// Try incremental update for supported languages
			if project.SupportsIncrementalIndexing(lang) {
				if tryIncrementalIndex(repoRoot, ckbDir, lang) {
					lastCommit = currentCommit
					fmt.Println("Watching for changes...")
					continue
				}
			}

			// Fall back to checking freshness and full reindex if needed
			meta, err := index.LoadMeta(ckbDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Could not load index metadata: %v\n", err)
				continue
			}

			if meta != nil {
				freshness := meta.CheckFreshness(repoRoot)
				if !freshness.Fresh {
					fmt.Printf("Index stale: %s\n", freshness.Reason)
					fmt.Println("Run 'ckb index --force' to rebuild.")
					// Don't update lastCommit — keep retrying on next tick
					continue
				}
			}

			lastCommit = currentCommit
			fmt.Println("Watching for changes...")
		}
	}
}

// getCurrentCommit returns the current HEAD commit hash.
func getCurrentCommit(repoRoot string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
