package symbols

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// DirOptions controls ExtractDirectoryWithOptions.
type DirOptions struct {
	// Filter, if non-nil, is called with the absolute file path; false skips it.
	Filter func(string) bool

	// RepoRoot is the repository root that Exclude globs are relative to.
	// Defaults to the extraction root when empty (matters when extracting a
	// sub-directory scope).
	RepoRoot string

	// Exclude is a list of globs matched against the repo-relative,
	// slash-separated path. See MatchExclude for the exact syntax.
	Exclude []string
}

// MatchExclude reports whether the repo-relative slash path rel matches any
// of the exclude globs. Exactly two forms are supported:
//
//   - "dir/**" (a pattern ending in "/**"): matches dir itself and everything
//     below it. The part before "/**" is a literal path prefix, compared on
//     whole path segments ("build/**" does not match "builder/x.js").
//   - anything else is handed to path.Match against the full relative path, so
//     "*" does not cross "/" ("*.min.js" only matches at the repo root;
//     use "src/*.min.js" or "src/gen/*").
//
// There is no leading-"**" support and no negation. Invalid patterns never match.
func MatchExclude(globs []string, rel string) bool {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	for _, g := range globs {
		g = strings.Trim(filepath.ToSlash(strings.TrimSpace(g)), "/")
		if g == "" {
			continue
		}
		if prefix, ok := strings.CutSuffix(g, "/**"); ok {
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				return true
			}
			continue
		}
		if ok, err := path.Match(g, rel); err == nil && ok {
			return true
		}
	}
	return false
}

// skipDirName reports whether a single directory name is always skipped.
func skipDirName(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__"
}

// hasSkippedDir reports whether any directory component of the slash path rel
// (excluding the final file name) is always skipped.
func hasSkippedDir(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if skipDirName(p) {
			return true
		}
	}
	return false
}

// gitListFiles returns the files git considers part of the work tree under
// root: tracked plus untracked-but-not-ignored (nested .gitignore files and
// global excludes are honoured by git itself). Paths are slash-separated and
// relative to root. An error means root is not in a git work tree or git is
// unavailable; callers should fall back to walking the directory.
func gitListFiles(ctx context.Context, root string) ([]string, error) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, gitBin, "ls-files", "-co", "--exclude-standard", "-z")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	return files, nil
}

// listSourceFiles returns absolute paths of candidate files under root,
// applying the built-in directory skips and opts.Exclude. It prefers
// `git ls-files` (so .gitignore is honoured) and falls back to a directory walk.
func listSourceFiles(ctx context.Context, root string, opts DirOptions) ([]string, error) {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = root
	}
	excluded := func(abs string) bool {
		if len(opts.Exclude) == 0 {
			return false
		}
		rel, err := filepath.Rel(repoRoot, abs)
		if err != nil {
			return false
		}
		return MatchExclude(opts.Exclude, rel)
	}

	var result []string
	if rels, err := gitListFiles(ctx, root); err == nil {
		seen := make(map[string]bool, len(rels))
		for _, rel := range rels {
			if seen[rel] || hasSkippedDir(rel) {
				continue
			}
			seen[rel] = true
			abs := filepath.Join(root, filepath.FromSlash(rel))
			if excluded(abs) {
				continue
			}
			// Skip files deleted from disk, symlinked dirs and submodules.
			if info, statErr := os.Stat(abs); statErr != nil || info.IsDir() {
				continue
			}
			result = append(result, abs)
		}
		return result, nil
	}

	err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // Intentionally skip walk errors and continue
		}
		if info.IsDir() {
			if p != root && (skipDirName(info.Name()) || excluded(p)) {
				return filepath.SkipDir
			}
			return nil
		}
		if excluded(p) {
			return nil
		}
		result = append(result, p)
		return nil
	})
	return result, err
}
