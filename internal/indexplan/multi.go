package indexplan

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// Target is one language to index and the manifest it was found from.
type Target struct {
	Lang     project.Language
	Manifest string
}

// minScriptFiles is how many tracked .py files without a Python manifest make
// Python a language of the repo. Fewer are a helper script or two beside the
// real project, not worth asking for an indexer.
const minScriptFiles = 5

// DetectTargets returns every language of repoRoot that CKB indexes, the
// primary first (see project.DetectAllLanguages). TypeScript and JavaScript are
// one target, since scip-typescript indexes both. Python also counts without a
// manifest when the repo tracks a directory of scripts: scip-python indexes
// them as they are, and a manifest-only search misses them next to a go.mod or
// package.json.
func DetectTargets(repoRoot string) []Target {
	primary, primaryManifest, all := project.DetectAllLanguages(repoRoot)
	if primary == project.LangUnknown {
		return nil
	}
	targets := []Target{{Lang: primary, Manifest: primaryManifest}}
	seen := map[project.Language]bool{primary: true}
	if primary == project.LangJavaScript || primary == project.LangTypeScript {
		seen[project.LangJavaScript], seen[project.LangTypeScript] = true, true
	}
	for _, lang := range all {
		if seen[lang] {
			continue
		}
		seen[lang] = true
		if lang == project.LangTypeScript || lang == project.LangJavaScript {
			seen[project.LangJavaScript], seen[project.LangTypeScript] = true, true
		}
		targets = append(targets, Target{Lang: lang, Manifest: project.FindManifestForLanguage(repoRoot, lang)})
	}
	if !seen[project.LangPython] {
		if n := trackedScripts(repoRoot); n >= minScriptFiles {
			targets = append(targets, Target{Lang: project.LangPython, Manifest: fmt.Sprintf("%d tracked .py files, no manifest", n)})
		}
	}
	return targets
}

// trackedScripts counts git-tracked .py files outside dependency and fixture
// directories.
func trackedScripts(repoRoot string) int {
	cmd := exec.Command("git", "ls-files", "-z", "--", "*.py")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, path := range strings.Split(string(out), "\x00") {
		if !strings.HasSuffix(path, ".py") {
			continue
		}
		p := "/" + path
		if strings.Contains(p, "/node_modules/") || strings.Contains(p, "/vendor/") ||
			strings.Contains(p, "/testdata/") || strings.Contains(p, "/site-packages/") {
			continue
		}
		n++
	}
	return n
}

// ErrNoOutput means an indexer exited cleanly without writing an index.
var ErrNoOutput = errors.New("indexer completed but wrote no index")

// LangResult is how one plan's indexer run went.
type LangResult struct {
	Plan     *Plan
	Duration time.Duration
	// Err is the indexer's failure, a Finalize failure, or ErrNoOutput.
	Err error
}

// RunAll runs every plan's indexer with run, finalizes each output (path
// rebasing, the Python import repair) and leaves one index at the plans'
// shared IndexPath holding all of them. With one plan that is exactly the
// single-language build. With several, each language's index is written to a
// part file beside the index and the parts are merged (scip.MergeIndexes), so
// a language whose indexer fails costs only its own documents.
//
// The previous index stays at IndexPath until the merged one is renamed over
// it, so a session opening the index mid-build reads the old one, never a
// missing file or one language's part. The exception is an indexer that can't
// be told where to write (scip-php, see writingTo): while it runs, the
// previous index sits aside and IndexPath is its output.
//
// The error is non-nil only when no language made it into the index, or the
// merge failed; then the previous index, if any, is still in place (multi
// only: a single indexer overwrites it as it always has).
func RunAll(repoRoot string, plans []*Plan, run func(*Plan) error) ([]LangResult, error) {
	if len(plans) == 0 {
		return nil, fmt.Errorf("no index plans")
	}
	indexPath := plans[0].IndexPath

	if len(plans) == 1 {
		res := runOne(repoRoot, plans[0], run)
		return []LangResult{res}, res.Err
	}

	results := make([]LangResult, 0, len(plans))
	var parts []string
	defer func() {
		for _, p := range parts {
			_ = os.Remove(p)
		}
	}()
	for _, plan := range plans {
		part := fmt.Sprintf("%s.%s.part", indexPath, plan.Lang)
		// A stale part from an interrupted build must not pass for output.
		_ = os.Remove(part)
		var res LangResult
		if redirected := plan.writingTo(part); redirected != nil {
			res = runOne(repoRoot, redirected, run)
			res.Plan = plan
		} else {
			res = runInPlace(repoRoot, plan, part, run)
		}
		if res.Err == nil {
			parts = append(parts, part)
		} else {
			_ = os.Remove(part)
		}
		results = append(results, res)
	}

	if len(parts) == 0 {
		return results, firstErr(results)
	}
	if err := scip.MergeIndexes(indexPath, parts); err != nil {
		return results, fmt.Errorf("merging the language indexes: %w", err)
	}
	return results, nil
}

// runInPlace runs an indexer that writes to IndexPath and moves its output to
// part. The previous index moves aside first, so an indexer that exits 0
// without writing can't have it taken for its output, and comes back after.
func runInPlace(repoRoot string, plan *Plan, part string, run func(*Plan) error) LangResult {
	backup := plan.IndexPath + ".prev"
	hadPrevious := os.Rename(plan.IndexPath, backup) == nil
	res := runOne(repoRoot, plan, run)
	if res.Err == nil {
		if err := os.Rename(plan.IndexPath, part); err != nil {
			res.Err = fmt.Errorf("moving the %s index aside: %w", plan.Lang, err)
		}
	}
	if hadPrevious {
		_ = os.Rename(backup, plan.IndexPath)
	} else {
		_ = os.Remove(plan.IndexPath)
	}
	return res
}

// writingTo is plan with its indexer told to write to path, or nil when the
// indexer can't be told (project.OutputFlag is empty: scip-php). The path is
// passed relative to the indexer's directory: commands are split on spaces,
// and a repo path may contain them.
func (p *Plan) writingTo(path string) *Plan {
	flag := project.OutputFlag(p.Lang)
	if p.base == "" || flag == "" {
		return nil
	}
	rel, err := filepath.Rel(p.IndexerDir, path)
	if err != nil || strings.ContainsAny(rel, " \t") {
		return nil
	}
	q := *p
	q.IndexPath = path
	q.Command = fmt.Sprintf("%s %s %s", p.base, flag, filepath.ToSlash(rel))
	q.NamesOutput = true
	return &q
}

func runOne(repoRoot string, plan *Plan, run func(*Plan) error) LangResult {
	start := time.Now()
	res := LangResult{Plan: plan}
	defer project.UndoInferredTSConfig(plan.IndexerDir, plan.Command)()
	if err := run(plan); err != nil {
		res.Err = err
	} else if err := Finalize(repoRoot, plan); err != nil {
		res.Err = err
	} else if _, err := os.Stat(plan.IndexPath); err != nil {
		res.Err = fmt.Errorf("%w at %s", ErrNoOutput, filepath.Base(plan.IndexPath))
	}
	res.Duration = time.Since(start)
	return res
}

func firstErr(results []LangResult) error {
	for _, r := range results {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}
