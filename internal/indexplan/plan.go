// Package indexplan is the one recipe for building a SCIP index: which command to
// run, from which directory, where the output goes, and how to turn what the
// indexer wrote into a repo-root index. `ckb index`, watch mode, MCP reindex and
// the daemon all go through it; each of them hand-rolling this is what let a C++
// --compdb-path flag, a custom scip.indexPath or a nested manifest work on one
// path and silently produce an unusable index on another.
package indexplan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/incremental"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// manifestFromFlag is what a caller passes as the manifest when the language came
// from --lang and no manifest was found for it.
const manifestFromFlag = "(specified via --lang)"

// Plan is the fully-resolved recipe for building a SCIP index for one language.
type Plan struct {
	Lang       project.Language
	Manifest   string
	Indexer    *project.IndexerInfo
	Command    string
	IndexerDir string
	IndexPath  string
}

// FailureKind says why no plan could be built.
type FailureKind int

const (
	// FailureIndexerMissing means there's no SCIP indexer for the language, or it
	// isn't installed. Recoverable: git-based features still work.
	FailureIndexerMissing FailureKind = iota + 1
	// FailureRequirementMissing means a prerequisite other than the indexer is
	// missing (compile_commands.json, bundler, composer, ...).
	FailureRequirementMissing
	// FailureError means something unrelated to indexer availability failed.
	FailureError
)

// Failure is why Build returned no plan.
type Failure struct {
	Kind    FailureKind
	Message string
	Err     error
}

// ResolveIndexPath resolves the SCIP index output path for repoRoot: the
// configured scip.indexPath if set, else the default index.scip at the repo root.
// Always returns an absolute path.
func ResolveIndexPath(repoRoot string) string {
	indexPath := "index.scip"
	if cfg, err := config.LoadConfig(repoRoot); err == nil && cfg.Backends.Scip.IndexPath != "" {
		indexPath = cfg.Backends.Scip.IndexPath
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(repoRoot, indexPath)
	}
	return indexPath
}

// IndexerInstalled reports whether an indexer's command is on PATH.
func IndexerInstalled(command string) bool {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}
	_, err := exec.LookPath(parts[0])
	return err == nil
}

// IsNested reports whether a manifest lives in a subdirectory of the repo.
// Incremental indexers run from the repo root, so with a nested manifest they find
// no project there and still exit cleanly, reporting an empty update over an index
// that has gone stale: only a full index works.
func IsNested(repoRoot string, lang project.Language) bool {
	return filepath.Dir(project.FindManifestForLanguage(repoRoot, lang)) != "."
}

// Build builds the indexer command for lang/manifest given the resolved index
// output path (see ResolveIndexPath) and an optional C/C++ compdb override (empty
// means auto-detect). The returned warning is a non-fatal note (currently only
// PHP's missing composer.lock) that may be set whether or not a plan comes back.
func Build(repoRoot string, lang project.Language, manifest, compdbOverride, indexPath string) (plan *Plan, warning string, failure *Failure) {
	indexer := project.GetIndexerInfo(lang)
	if indexer == nil {
		return nil, "", &Failure{Kind: FailureIndexerMissing, Message: fmt.Sprintf("no SCIP indexer available for %s", project.LanguageDisplayName(lang))}
	}

	// Check if using non-default index path (requires --output flag)
	defaultIndexPath := filepath.Join(repoRoot, "index.scip")
	needsOutputFlag := indexPath != defaultIndexPath

	// Ensure output directory exists (for custom paths)
	if needsOutputFlag {
		outputDir := filepath.Dir(indexPath)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return nil, "", &Failure{Kind: FailureError, Message: fmt.Sprintf("could not create index directory: %v", err), Err: err}
		}
	}

	var command string

	// Build command - some languages need special handling
	switch lang {
	case project.LangCpp:
		cppCmd, err := project.BuildCppCommand(repoRoot, compdbOverride)
		if err != nil || cppCmd == "" {
			return nil, "", &Failure{Kind: FailureRequirementMissing, Message: "compile_commands.json not found (generate with cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON -B build)"}
		}
		command = cppCmd

	case project.LangRuby:
		rubyCmd, err := project.BuildRubyCommand(repoRoot)
		if err != nil {
			return nil, "", &Failure{Kind: FailureRequirementMissing, Message: "bundle not found (install with: gem install bundler)"}
		}
		command = rubyCmd

	case project.LangPHP:
		w, err := project.ValidatePHPSetup(repoRoot)
		warning = w
		if err != nil {
			return nil, warning, &Failure{Kind: FailureRequirementMissing, Message: "scip-php not installed (composer require --dev davidrjenni/scip-php && composer install)"}
		}
		command = indexer.Command

	default:
		// Standard languages: use base command as-is.
		command = indexer.Command
	}

	if needsOutputFlag {
		command = fmt.Sprintf("%s --output %s", command, indexPath)
	}

	if !IndexerInstalled(indexer.CheckCommand) {
		return nil, warning, &Failure{Kind: FailureIndexerMissing, Message: fmt.Sprintf("%s not installed (install with: %s)", indexer.CheckCommand, indexer.InstallCommand)}
	}

	// Run the indexer from the manifest's directory.
	// For monorepos, the manifest may be in a subdirectory (e.g., src/cli/go.mod).
	indexerDir := repoRoot
	if manifest != "" && manifest != manifestFromFlag {
		manifestDir := filepath.Dir(filepath.Join(repoRoot, manifest))
		if manifestDir != repoRoot {
			indexerDir = manifestDir
		}
	}

	return &Plan{
		Lang:       lang,
		Manifest:   manifest,
		Indexer:    indexer,
		Command:    command,
		IndexerDir: indexerDir,
		IndexPath:  indexPath,
	}, warning, nil
}

// Finalize makes the output of an indexer that ran in a subdirectory (a nested
// manifest such as engine/Cargo.toml) look like a repo-root index. Indexers write
// index.scip into their working directory unless told otherwise, and they record
// document paths relative to that directory; every CKB consumer expects the
// configured index path and repo-relative document paths.
func Finalize(repoRoot string, plan *Plan) error {
	if plan.IndexerDir == repoRoot {
		return nil
	}
	written := filepath.Join(plan.IndexerDir, "index.scip")
	if written != plan.IndexPath {
		if _, err := os.Stat(written); err == nil {
			// Replaces the previous run's index at the target path.
			if err := os.Rename(written, plan.IndexPath); err != nil {
				return fmt.Errorf("moving %s to %s: %w", written, plan.IndexPath, err)
			}
		} else if plan.IndexPath == filepath.Join(repoRoot, "index.scip") {
			// Nothing new was written. Whatever sits at the target is an earlier,
			// already rebased index and must not be rebased again.
			return nil
		}
	}
	prefix, err := filepath.Rel(repoRoot, plan.IndexerDir)
	if err != nil {
		return fmt.Errorf("resolving indexer directory: %w", err)
	}
	if err := scip.RebaseDocumentPaths(plan.IndexPath, filepath.ToSlash(prefix), repoRoot); err != nil {
		return fmt.Errorf("rewriting index paths: %w", err)
	}
	return nil
}

// IncrementalConfig is the incremental indexer's config for a repo, with the
// configured index path: the default config would patch the wrong file.
func IncrementalConfig(repoRoot string) *incremental.Config {
	indexPath := "index.scip"
	if cfg, err := config.LoadConfig(repoRoot); err == nil && cfg.Backends.Scip.IndexPath != "" {
		indexPath = cfg.Backends.Scip.IndexPath
	}
	return &incremental.Config{
		IndexPath:            indexPath,
		IncrementalThreshold: 50,
		IndexTests:           false,
	}
}
