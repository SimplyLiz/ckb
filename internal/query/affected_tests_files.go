package query

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// reasonUsesChangedFile marks a test found only because it uses something defined
// in a changed file. The symbol it uses may not be one the diff touched: a change
// inside an object literal is attributed to the literal's members, not to the
// exported store or schema around them, and a test reaching the store through the
// export is affected all the same.
const reasonUsesChangedFile = "uses-changed-file"

// changedSourceFiles returns the production files the diff changed in more than
// comments or whitespace.
func changedSourceFiles(changed []impact.ChangedSymbol) []string {
	seen := make(map[string]bool)
	var files []string
	for _, sym := range changed {
		if sym.Trivial || sym.ChangeType == impact.ChangeDeleted || isTestFilePath(sym.File) || seen[sym.File] {
			continue
		}
		seen[sym.File] = true
		files = append(files, sym.File)
	}
	sort.Strings(files)
	return files
}

// testsUsingFiles returns, for every runnable test file that uses a symbol defined
// in one of files, the files it uses. A namespace symbol (the module itself, which
// an import names) does not count: a test has to use something the file defines.
func testsUsingFiles(idx *scip.SCIPIndex, files []string) map[string][]string {
	if idx == nil {
		return nil
	}
	tests := make(map[string][]string)
	for _, file := range files {
		doc := idx.GetDocument(file)
		if doc == nil {
			continue
		}
		reached := make(map[string]bool)
		for _, sym := range doc.Symbols {
			if sym == nil || strings.HasPrefix(sym.Symbol, "local ") || strings.HasSuffix(sym.Symbol, "/") {
				continue
			}
			for _, ref := range idx.RefIndex[sym.Symbol] {
				if ref == nil || ref.Doc == nil || ref.Occ == nil || ref.Occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
					continue
				}
				path := ref.Doc.RelativePath
				if path == file || reached[path] || !isRunnableTestPath(path) {
					continue
				}
				reached[path] = true
				tests[path] = append(tests[path], file)
			}
		}
	}
	return tests
}

// mergeFileLevelTests adds the tests that use a changed file to the ones that use
// a changed symbol. A test already found keeps its reason.
func mergeFileLevelTests(tests []AffectedTest, byFile map[string][]string) []AffectedTest {
	have := make(map[string]bool, len(tests))
	for _, t := range tests {
		have[t.FilePath] = true
	}
	paths := make([]string, 0, len(byFile))
	for path := range byFile {
		if !have[path] {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		used := byFile[path]
		if len(used) > 5 {
			used = used[:5]
		}
		tests = append(tests, AffectedTest{
			FilePath:   path,
			Reason:     reasonUsesChangedFile,
			AffectedBy: used,
			Confidence: 0.5,
		})
	}
	return tests
}

// isRunnableTestPath reports whether a test runner picks the file up as a test,
// not just whether it is test code. In JavaScript and TypeScript a helper under
// tests/ (fixtures.ts) is test code but no test: vitest and jest only run files
// named *.test.* / *.spec.* (jest also anything under __tests__). Other languages
// keep isTestFilePath's directory rule, which is how their runners work (Rust
// tests/*.rs, Python's test_ prefix and the like).
func isRunnableTestPath(path string) bool {
	if !isTestFilePath(path) {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts":
		base := strings.ToLower(filepath.Base(path))
		return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
			strings.Contains("/"+strings.ToLower(filepath.ToSlash(path)), "/__tests__/")
	}
	return true
}
