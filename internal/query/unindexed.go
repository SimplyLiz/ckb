package query

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// sourceLanguageOf names the language a source file is indexed as, by extension,
// or "" for anything that isn't source.
func sourceLanguageOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return "typescript"
	case ".py", ".pyi":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".dart":
		return "dart"
	case ".rb":
		return "ruby"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp":
		return "cpp"
	}
	return ""
}

// UnindexedLanguages says which languages have tracked source files under scope
// (the whole repo if empty) but no document at all in the SCIP index, as
// "typescript (5 files)". A lookup there finds nothing because nothing was indexed,
// not because nothing exists: an index is built for one language at a time. Without
// an index, or outside git, it says nothing.
func (e *Engine) UnindexedLanguages(scope string) []string {
	if e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return nil
	}
	idx := e.scipAdapter.GetIndex()
	if idx == nil {
		return nil
	}
	indexed := make(map[string]bool)
	for _, doc := range idx.Documents {
		if lang := sourceLanguageOf(doc.RelativePath); lang != "" {
			indexed[lang] = true
		}
	}
	args := []string{"-C", e.repoRoot, "ls-files", "-z"}
	if scope = strings.Trim(filepath.ToSlash(scope), "/"); scope != "" {
		args = append(args, "--", scope)
	}
	out, err := exec.Command("git", args...).Output() // #nosec G204 -- fixed git subcommand; scope is a pathspec argument
	if err != nil {
		return nil
	}
	counts := make(map[string]int)
	for _, path := range strings.Split(string(out), "\x00") {
		if lang := sourceLanguageOf(path); lang != "" && !indexed[lang] && !strings.Contains("/"+path, "/node_modules/") {
			counts[lang]++
		}
	}
	var langs []string
	for lang, n := range counts {
		langs = append(langs, fmt.Sprintf("%s (%s)", lang, plural(n, "file")))
	}
	sort.Strings(langs)
	return langs
}

// UnindexedNote is the warning for an empty lookup in a scope whose language the
// index doesn't cover, or "".
func (e *Engine) UnindexedNote(scope string) string {
	langs := e.UnindexedLanguages(scope)
	if len(langs) == 0 {
		return ""
	}
	where := "this repo"
	if scope != "" {
		where = scope
	}
	return fmt.Sprintf("%s has files the index doesn't cover: %s. Nothing found there means not indexed, not absent; read those files directly", where, strings.Join(langs, ", "))
}
