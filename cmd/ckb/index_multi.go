package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/indexplan"
	"github.com/SimplyLiz/CodeMCP/internal/project"
	"github.com/SimplyLiz/CodeMCP/internal/repostate"
)

// languagePlan is the build planned for a repo's languages: the indexers that
// can run, and the languages left out with why. 'ckb index', the MCP reindex,
// watch mode and the missing-index diagnosis all plan through planLanguages, so
// they agree on which languages get indexed.
type languagePlan struct {
	Targets []indexplan.Target
	Plans   []*indexPlan
	// Files counts each planned language's source files.
	Files map[project.Language]int
	// Skipped are the targets with no plan. Failures holds the plan failure
	// behind each one that has one, in the same order, for the CLI's
	// install instructions; a large-repo skip has none.
	Skipped  []index.SkippedLanguage
	Failures []indexResult
	// LargeOnly means every target was skipped for size alone.
	LargeOnly bool
	// Warnings are the plan builder's non-fatal notes (PHP's composer.lock).
	Warnings []string
}

// planLanguages builds the index plan of every target. A language over
// scipLargeRepoThreshold is skipped unless allowLarge; the limit is per
// language, so a huge TypeScript tree doesn't cost the Go half its index.
func planLanguages(repoRoot string, targets []indexplan.Target, compdbOverride, indexPath string, allowLarge bool) *languagePlan {
	lp := &languagePlan{Targets: targets, Files: make(map[project.Language]int)}
	large := 0
	for _, t := range targets {
		n := countSourceFiles(repoRoot, t.Lang)
		name := project.LanguageDisplayName(t.Lang)
		if n >= scipLargeRepoThreshold && !allowLarge {
			large++
			lp.Skipped = append(lp.Skipped, index.SkippedLanguage{
				Language: string(t.Lang),
				Files:    n,
				Reason:   fmt.Sprintf("%d %s files, over the %d CKB indexes without being asked", n, name, scipLargeRepoThreshold),
				Fix:      "ckb index --scip",
			})
			lp.Failures = append(lp.Failures, indexResult{Outcome: indexOutcomeSkippedLargeRepo, Lang: t.Lang})
			continue
		}
		plan, res := buildIndexPlan(repoRoot, t.Lang, t.Manifest, compdbOverride, indexPath)
		if res.Warning != "" {
			lp.Warnings = append(lp.Warnings, res.Warning)
		}
		if plan == nil {
			skip := index.SkippedLanguage{Language: string(t.Lang), Files: n, Reason: res.Message}
			if res.Outcome == indexOutcomeIndexerMissing {
				if info := project.GetIndexerInfo(t.Lang); info != nil {
					skip.Fix = info.InstallCommand
					if f := strings.Fields(info.CheckCommand); len(f) > 0 {
						skip.Indexer = f[0]
					}
				}
			}
			lp.Skipped = append(lp.Skipped, skip)
			lp.Failures = append(lp.Failures, res)
			continue
		}
		lp.Plans = append(lp.Plans, plan)
		lp.Files[t.Lang] = n
	}
	lp.LargeOnly = len(targets) > 0 && large == len(targets)
	return lp
}

// languageNames lists the display names of langs.
func languageNames(langs []project.Language) string {
	names := make([]string, 0, len(langs))
	for _, l := range langs {
		names = append(names, project.LanguageDisplayName(l))
	}
	return strings.Join(names, ", ")
}

// skippedNote is one line per skipped language, "Python: scip-python not
// installed (install with: ...)", for warnings and logs.
func skippedNote(skipped []index.SkippedLanguage) string {
	parts := make([]string, 0, len(skipped))
	for _, s := range skipped {
		parts = append(parts, fmt.Sprintf("%s: %s", project.LanguageDisplayName(project.Language(s.Language)), s.Reason))
	}
	return strings.Join(parts, "; ")
}

// indexedTargets resolves the languages a background build (watch mode, the
// first index 'ckb mcp' starts, the reindex tool) covers. A language chosen
// with 'ckb index --lang' stays the only one; a project.json from before
// multi-language indexing keeps its one language until 'ckb index' runs, so a
// running session never starts an indexer nobody asked for. Otherwise every
// detected language. ok is false when there is none.
func indexedTargets(repoRoot string) ([]indexplan.Target, bool) {
	if cfg, err := project.LoadConfig(repoRoot); err == nil {
		if cfg.LanguageFromFlag || len(cfg.Languages) == 0 {
			langs := cfg.IndexLanguages()
			if len(langs) > 0 {
				targets := make([]indexplan.Target, 0, len(langs))
				for _, l := range langs {
					targets = append(targets, indexplan.Target{Lang: l, Manifest: project.FindManifestForLanguage(repoRoot, l)})
				}
				return targets, true
			}
		}
	}
	targets := indexplan.DetectTargets(repoRoot)
	return targets, len(targets) > 0
}

// recordIndexBuild writes project.json and the index metadata after a build:
// the languages in the index, the ones left out, and the git state.
func recordIndexBuild(repoRoot, ckbDir string, lp *languagePlan, results []indexplan.LangResult, duration time.Duration,
	fromFlag bool, refresh *index.LastRefresh) (*index.IndexMeta, []error) {
	var errs []error
	var built []*indexPlan
	meta := &index.IndexMeta{
		CreatedAt:   time.Now(),
		Duration:    duration.Round(time.Millisecond * 100).String(),
		LastRefresh: refresh,
		Skipped:     append([]index.SkippedLanguage(nil), lp.Skipped...),
	}
	var indexers []string
	for _, r := range results {
		lang := r.Plan.Lang
		if r.Err != nil {
			meta.Skipped = append(meta.Skipped, index.SkippedLanguage{
				Language: string(lang),
				Files:    lp.Files[lang],
				Reason:   fmt.Sprintf("%s failed: %v", r.Plan.Indexer.CheckCommand, r.Err),
				Fix:      "ckb index",
			})
			continue
		}
		built = append(built, r.Plan)
		meta.FileCount += lp.Files[lang]
		meta.Languages = append(meta.Languages, index.LanguageIndex{
			Language: string(lang),
			Indexer:  r.Plan.Indexer.CheckCommand,
			Files:    lp.Files[lang],
		})
		indexers = append(indexers, r.Plan.Indexer.CheckCommand)
	}
	if len(built) == 0 {
		return nil, nil
	}
	meta.Indexer = strings.Join(indexers, ", ")
	meta.IndexerArgs = strings.Fields(built[0].Command)

	if rs, err := repostate.ComputeRepoState(repoRoot); err == nil {
		meta.CommitHash = rs.HeadCommit
		meta.RepoStateID = rs.RepoStateID
	}
	if err := meta.Save(ckbDir); err != nil {
		errs = append(errs, fmt.Errorf("could not save index metadata: %w", err))
	}

	langs := make([]project.Language, 0, len(lp.Targets))
	for _, t := range lp.Targets {
		langs = append(langs, t.Lang)
	}
	primary := built[0]
	if err := project.SaveConfig(repoRoot, &project.ProjectConfig{
		Language:         primary.Lang,
		Indexer:          primary.Indexer.CheckCommand,
		ManifestPath:     primary.Manifest,
		DetectedAt:       time.Now(),
		Languages:        langs,
		LanguageFromFlag: fromFlag,
	}); err != nil {
		errs = append(errs, fmt.Errorf("could not save project config: %w", err))
	}
	return meta, errs
}

// tracksIncrementally reports whether the incremental tracking tables should
// be filled after a full build: when any indexed language has an incremental
// indexer. The tables mirror the whole index, so they stay valid for a merged
// one; only the incremental update itself is single-language.
func tracksIncrementally(meta *index.IndexMeta) bool {
	for _, l := range meta.Languages {
		if project.SupportsIncrementalIndexing(project.Language(l.Language)) {
			return true
		}
	}
	return false
}

// targetLanguages lists the languages of targets.
func targetLanguages(targets []indexplan.Target) []project.Language {
	langs := make([]project.Language, 0, len(targets))
	for _, t := range targets {
		langs = append(langs, t.Lang)
	}
	return langs
}

// writeIndexLanguages adds the per-language coverage to 'ckb status': one line
// per language in the index when there are several, and one per language left
// out, with the fix.
func writeIndexLanguages(b *strings.Builder, st *IndexStatusCLI) {
	if len(st.Languages) > 1 || len(st.Skipped) > 0 {
		for _, l := range st.Languages {
			fmt.Fprintf(b, "  ✓ %s: %d files (%s)\n", project.LanguageDisplayName(project.Language(l.Language)), l.Files, l.Indexer)
		}
	}
	for _, s := range st.Skipped {
		fmt.Fprintf(b, "  ✗ %s not indexed: %s\n", project.LanguageDisplayName(project.Language(s.Language)), s.Reason)
		if s.Fix != "" && !strings.Contains(s.Reason, s.Fix) {
			fmt.Fprintf(b, "    Fix: %s\n", s.Fix)
		}
	}
}
