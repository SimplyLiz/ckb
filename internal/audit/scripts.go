package audit

import (
	"bufio"
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/modules"
)

// scriptLanguages are the languages whose imports the audit can resolve to a
// file, which it needs to tell a script nobody imports from a helper module.
var scriptLanguages = map[string]string{
	".py":  modules.LanguagePython,
	".ts":  modules.LanguageTypeScript,
	".tsx": modules.LanguageTypeScript,
	".js":  modules.LanguageJavaScript,
	".jsx": modules.LanguageJavaScript,
}

// standaloneScripts are the files among files that are run, not imported: a
// shebang, Python's __main__ guard or, outside a Python package, work done at
// module level (a loop, a with block, a bare call) says so, and no other file
// in files imports them. Their missing test file is a property of a one-off
// script, not a risk to point at; a helper the scripts share still counts.
// Only languages in scriptLanguages qualify, since for the others "nobody
// imports it" can't be checked. A docstring line that reads like a call
// passes for module-level work; the import check still keeps such a file in
// when anything imports it.
func standaloneScripts(repoRoot string, files []string, logger *slog.Logger) map[string]bool {
	candidates := map[string]bool{}
	for _, f := range files {
		if _, ok := scriptLanguages[filepath.Ext(f)]; ok && runsDirectly(filepath.Join(repoRoot, f)) {
			candidates[filepath.ToSlash(f)] = true
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	scanner := modules.NewImportScanner(&config.ImportScanConfig{Enabled: true, MaxFileSizeBytes: 1000000, SkipBinary: true}, logger)
	classifiers := map[string]*modules.ImportClassifier{}
	for _, f := range files {
		lang, ok := scriptLanguages[filepath.Ext(f)]
		if !ok {
			continue
		}
		edges, err := scanner.ScanFile(filepath.Join(repoRoot, f), repoRoot)
		if err != nil {
			continue
		}
		c := classifiers[lang]
		if c == nil {
			c = modules.NewImportClassifier(&modules.ModuleContext{RepoRoot: repoRoot, Language: lang})
			classifiers[lang] = c
		}
		from := filepath.ToSlash(f)
		for _, e := range edges {
			to := filepath.ToSlash(c.ResolveImport(e.To, from))
			if to == "" || to == from {
				continue
			}
			delete(candidates, to)
			if lang != modules.LanguagePython {
				for _, file := range nodeFiles(to) {
					delete(candidates, file)
				}
			}
		}
	}
	return candidates
}

// nodeFiles are the files a resolved JS/TS import path can name: "./run"
// is run.ts, run.js, run/index.ts..., and "./run.js" in TypeScript is run.ts.
func nodeFiles(path string) []string {
	exts := []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}
	out := make([]string, 0, 2*len(exts))
	for _, ext := range exts {
		out = append(out, path+ext, path+"/index"+ext)
	}
	if stem, ok := strings.CutSuffix(path, ".js"); ok {
		out = append(out, stem+".ts", stem+".tsx")
	}
	return out
}

// runsDirectly reports whether the file starts with a shebang or, in Python,
// has an `if __name__ == "__main__":` guard or, outside a package (no
// __init__.py beside it), does its work at module level.
func runsDirectly(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	first := true
	python := strings.HasSuffix(path, ".py")
	topLevelCounts := python && !fileExists(filepath.Join(filepath.Dir(path), "__init__.py"))
	for s.Scan() {
		line := s.Bytes()
		if first && bytes.HasPrefix(line, []byte("#!")) {
			return true
		}
		first = false
		if !python {
			return false
		}
		if t := strings.TrimSpace(string(line)); strings.HasPrefix(t, "if __name__") && strings.Contains(t, "__main__") {
			return true
		}
		if topLevelCounts && isModuleLevelWork(string(line)) {
			return true
		}
	}
	return false
}

// isModuleLevelWork reports whether an unindented Python line does work when
// the file runs: a for, while or with block, or a call that is a statement of
// its own (main(sys.argv[1]), sys.path.insert(...)). Assignments, definitions,
// imports and if/try blocks are what modules have too.
func isModuleLevelWork(line string) bool {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return false
	}
	for _, kw := range []string{"for ", "while ", "with ", "async for ", "async with "} {
		if strings.HasPrefix(line, kw) {
			return true
		}
	}
	// A dotted name directly followed by "(", and no "=" before it.
	i := 0
	for i < len(line) && (line[i] == '_' || line[i] == '.' || isAlnum(line[i])) {
		i++
	}
	if i == 0 || i == len(line) || line[i] != '(' || line[0] == '.' || (line[0] >= '0' && line[0] <= '9') {
		return false
	}
	return true
}

func isAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
