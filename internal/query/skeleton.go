package query

import (
	"path/filepath"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
)

// GetRankedSkeleton returns project files ranked by PageRank relevance to a set of
// focus files, pruned to a token budget. Returns nil when Cartographer is not compiled in.
// Paths come back repo-relative even if the library reports absolute ones.
func (e *Engine) GetRankedSkeleton(focus []string, tokenBudget uint32) (*cartographer.RankedSkeletonResult, error) {
	if !cartographer.Available() {
		return nil, nil //nolint:nilnil // intentional: callers check nil to detect unavailability
	}
	res, err := cartographer.RankedSkeleton(e.repoRoot, focus, tokenBudget)
	if err != nil || res == nil {
		return res, err
	}
	for i := range res.Files {
		res.Files[i].Path = e.repoRelative(res.Files[i].Path)
	}
	return res, nil
}

// repoRelative strips the repo root from an absolute path inside the repo;
// other paths are returned unchanged.
func (e *Engine) repoRelative(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	for _, root := range []string{e.repoRoot, resolvedRoot(e.repoRoot)} {
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return p
}

func resolvedRoot(root string) string {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		return r
	}
	return root
}
