package query

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// Three ways a Rust change breaks code that the changed symbol's own references don't
// reach. All come from reading what real commits did to the uses the compiler rejected.

// rustIndex is what these checks need beyond references. The SCIP adapter has it; an
// index without it just doesn't get them.
type rustIndex interface {
	TypeSymbolIn(path, name string) string
	GeneratedMethods(typeSymbol, method string) []string
	VariantSymbolsIn(path, typeSymbol string) map[string]string
}

// rustOpen is the outcome for one declaration: the files with a use left open.
type rustOpen struct {
	name   string
	change impact.DeclarationChange
	files  map[string]bool
}

// checkRustFiles looks at what the diff did to struct fields and re-exports in each Rust
// file. Neither shows up as a changed symbol: a macro can swallow a struct's fields, an
// added field has no uses yet, and a `pub use` isn't a symbol at all.
func (c *doneChecker) checkRustFiles(ctx context.Context, adapter referenceFinder) []rustOpen {
	index, _ := adapter.(rustIndex)
	if adapter == nil || index == nil {
		return nil
	}
	paths := make([]string, 0, len(c.files))
	for path, file := range c.files {
		if strings.HasSuffix(path, ".rs") && path == file.OldPath && !file.Deleted && !file.IsNew {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)

	var open []rustOpen
	for _, path := range paths {
		file := c.files[path]
		oldLines := c.readOld(file, path)
		newLines, ok := c.readNew(file, path)
		if oldLines == nil || !ok {
			continue
		}
		open = append(open, c.checkStructFields(ctx, adapter, index, path, oldLines, newLines)...)
		open = append(open, c.checkEnumVariants(ctx, adapter, index, path, oldLines, newLines)...)
		open = append(open, c.checkReexports(ctx, path, oldLines, newLines)...)
	}
	return open
}

var rustStructNameRe = regexp.MustCompile(`^\s*(?:pub(?:\s*\([^)]*\))?\s+)?struct\s+([A-Za-z_]\w*)`)

// checkStructFields lists the uses a changed field set breaks: literals and patterns
// without `..`, and a constructor a macro generates from the fields, which the index
// knows only by its calls. Salsa's `Foo::new(db, a, b)` is one.
func (c *doneChecker) checkStructFields(ctx context.Context, adapter referenceFinder, index rustIndex, path string, oldLines, newLines []string) []rustOpen {
	var open []rustOpen
	seen := make(map[string]bool)
	for _, text := range oldLines {
		m := rustStructNameRe.FindStringSubmatch(text)
		if m == nil || seen[m[1]] {
			continue
		}
		name := m[1]
		seen[name] = true
		before, okBefore := diff.RustStructFields(oldLines, name)
		after, okAfter := diff.RustStructFields(newLines, name)
		if !okBefore || !okAfter || slices.Equal(before, after) {
			continue
		}
		id := index.TypeSymbolIn(path, name)
		if id == "" {
			continue
		}
		sym := impact.ChangedSymbol{SymbolID: id, Name: name, File: path}
		via := fmt.Sprintf("fields of %s changed", name)
		files := make(map[string]bool)
		c.checkUses(ctx, adapter, sym, useQuery{name: name, change: impact.DeclChanged, via: via, literal: true, lang: "rust"}, files)
		for _, ctor := range index.GeneratedMethods(id, "new") {
			ctorSym := impact.ChangedSymbol{SymbolID: ctor, Name: name + "::new", File: path}
			c.checkUses(ctx, adapter, ctorSym, useQuery{name: "new", change: impact.DeclChanged, via: via}, files)
		}
		if len(files) > 0 {
			open = append(open, rustOpen{name: name, change: impact.DeclChanged, files: files})
		}
	}
	return open
}

var rustEnumNameRe = regexp.MustCompile(`^\s*(?:pub(?:\s*\([^)]*\))?\s+)?enum\s+([A-Za-z_]\w*)`)

// checkEnumVariants lists the matches a new variant leaves incomplete: those over the
// enum with no arm for the rest and none for the variant. A removed variant is a removed
// symbol and checked as one. The matches are found from the old variants' references:
// an arm spelled `Self::A`, or `A` through a glob import, refers to the variant only,
// not to the enum. An index without variant symbols falls back to the enum's own.
func (c *doneChecker) checkEnumVariants(ctx context.Context, adapter referenceFinder, index rustIndex, path string, oldLines, newLines []string) []rustOpen {
	var open []rustOpen
	seen := make(map[string]bool)
	for _, text := range oldLines {
		m := rustEnumNameRe.FindStringSubmatch(text)
		if m == nil || seen[m[1]] {
			continue
		}
		name := m[1]
		seen[name] = true
		before, okBefore := diff.RustEnumVariants(oldLines, name)
		after, okAfter := diff.RustEnumVariants(newLines, name)
		if !okBefore || !okAfter {
			continue
		}
		var added []string
		for _, v := range after {
			if !slices.Contains(before, v) {
				added = append(added, v)
			}
		}
		if len(added) == 0 {
			continue
		}
		id := index.TypeSymbolIn(path, name)
		if id == "" {
			continue
		}
		sym := impact.ChangedSymbol{SymbolID: id, Name: name, File: path}
		via := fmt.Sprintf("%s gained %s; a match without it or `_` is incomplete", name, strings.Join(added, ", "))
		files := make(map[string]bool)
		query := useQuery{name: name, change: impact.DeclChanged, via: via, newVariants: added}
		variants := index.VariantSymbolsIn(path, id)
		if len(variants) == 0 {
			c.checkUses(ctx, adapter, sym, query, files)
		}
		for _, v := range before {
			if variants[v] == "" {
				continue
			}
			// Listed under the enum; each reference is found by the variant's name.
			query.name = v
			c.checkUses(ctx, adapter, impact.ChangedSymbol{SymbolID: variants[v], Name: name, File: path}, query, files)
		}
		if len(files) > 0 {
			open = append(open, rustOpen{name: name, change: impact.DeclChanged, files: files})
		}
	}
	return open
}

var rustFnRe = regexp.MustCompile(`\bfn\s+([A-Za-z_]\w*)`)

// checkRustAlias follows a retargeted type alias through the functions whose signature
// names it. Their callers hold the new type without ever spelling the alias:
// `f().ok()` stops compiling when f's `type R = Result<..>` becomes a struct. One level,
// and only aliases: a changed struct is still the same type to whoever holds one.
func (c *doneChecker) checkRustAlias(ctx context.Context, adapter referenceFinder, sym impact.ChangedSymbol) map[string]bool {
	if adapter == nil || !strings.HasSuffix(sym.File, ".rs") || !diff.IsRustTypeAlias(sym.DeclarationBefore) {
		return nil
	}
	name := leafName(sym.Name)
	refs, err := adapter.FindReferences(ctx, sym.SymbolID, backends.RefOptions{MaxResults: maxImpactReferences, IncludeTests: true})
	if err != nil || refs == nil {
		c.failed++
		return nil
	}
	files := make(map[string]bool)
	followed := make(map[string]bool)
	for i := range refs.References {
		ref := &refs.References[i]
		fn := ref.FromSymbol
		if fn == "" || followed[fn] || fn == sym.SymbolID || !c.inSignature(ref, name, leafName(ref.FromSymbolName)) {
			continue
		}
		followed[fn] = true
		fnSym := impact.ChangedSymbol{SymbolID: fn, Name: ref.FromSymbolName, File: ref.Location.Path}
		c.checkUses(ctx, adapter, fnSym, useQuery{
			name:   leafName(ref.FromSymbolName),
			change: impact.DeclChanged,
			via:    fmt.Sprintf("signature names %s, which changed", name),
		}, files)
	}
	return files
}

// inSignature says whether a reference is between `fn name` and the function's body.
func (c *doneChecker) inSignature(ref *backends.Reference, name, fn string) bool {
	if fn == "" {
		return false
	}
	file := c.files[ref.Location.Path]
	var lines []string
	if file != nil {
		lines = c.readOld(file, ref.Location.Path)
	} else {
		lines, _ = c.readNew(nil, ref.Location.Path)
	}
	line := ref.Location.Line
	if line < 1 || line > len(lines) {
		return false
	}
	for current := line; current >= 1 && current > line-maxSignatureLines; current-- {
		text := lines[current-1]
		if current == line {
			text = textBefore(text, name) // What comes after the use is no evidence
		}
		if m := rustFnRe.FindStringSubmatch(text); m != nil {
			return m[1] == fn
		}
		if strings.ContainsAny(text, "{};") {
			return false
		}
	}
	return false
}

const maxSignatureLines = 20

func textBefore(text, name string) string {
	if m := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).FindStringIndex(text); m != nil {
		return text[:m[0]]
	}
	return text
}

// checkReexports finds imports of a name this file no longer re-exports. Whatever the new
// side of a file still says about it is open, so these need no comparison.
func (c *doneChecker) checkReexports(_ context.Context, path string, oldLines, newLines []string) []rustOpen {
	before := diff.RustReexports(oldLines)
	if len(before) == 0 {
		return nil
	}
	after := diff.RustReexports(newLines)
	var open []rustOpen
	for name := range before {
		if after[name] {
			continue
		}
		files := make(map[string]bool)
		for _, module := range c.rustModulePaths(path) {
			c.grepReexportUses(path, module, name, files)
		}
		if len(files) > 0 {
			open = append(open, rustOpen{name: name, change: impact.DeclRemoved, files: files})
		}
	}
	return open
}

// rustModulePath is how a module is spelled in a `use`, and where that spelling works.
type rustModulePath struct {
	name string
	dir  string
}

// rustModulePaths returns what other code writes before a name this file exports:
// the module's own name, or for a crate root the crate's name and `crate` inside it.
func (c *doneChecker) rustModulePaths(path string) []rustModulePath {
	base := strings.TrimSuffix(filepath.Base(path), ".rs")
	dir := filepath.Dir(path)
	switch base {
	case "mod":
		return []rustModulePath{{name: filepath.Base(dir)}}
	case "lib", "main":
		crateDir := filepath.Dir(dir)
		paths := []rustModulePath{{name: "crate", dir: crateDir}}
		if crate := c.rustCrateName(crateDir); crate != "" {
			paths = append(paths, rustModulePath{name: crate})
		}
		return paths
	}
	return []rustModulePath{{name: base}}
}

var cargoNameRe = regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"`)

func (c *doneChecker) rustCrateName(dir string) string {
	manifest, err := os.ReadFile(filepath.Join(c.repoRoot, dir, "Cargo.toml"))
	if err != nil {
		return ""
	}
	m := cargoNameRe.FindSubmatch(manifest)
	if m == nil {
		return ""
	}
	return strings.ReplaceAll(string(m[1]), "-", "_")
}

// grepReexportUses adds the places still importing name through module, by path or glob.
func (c *doneChecker) grepReexportUses(definer string, module rustModulePath, name string, files map[string]bool) {
	const notWord = `[^A-Za-z0-9_]`
	mod, word := regexp.QuoteMeta(module.name), regexp.QuoteMeta(name)
	named := fmt.Sprintf(`(^|%s)%s::(\{(.*%s)?)?%s(%s|$)`, notWord, mod, notWord, word, notWord)
	for _, hit := range c.gitGrep(module.dir, named) {
		c.addReexportSite(definer, module.name, name, hit.path, hit.line, hit.text, files)
	}
	const space = `[[:space:]]` // git grep's -E doesn't know \s on every platform
	glob := fmt.Sprintf(`^%[1]s*(pub(\(.*\))?%[1]s+)?use%[1]s+(.*%[2]s)?%[3]s::\*`, space, notWord, mod)
	for _, hit := range c.gitGrep(module.dir, glob) {
		lines, ok := c.readNew(c.files[hit.path], hit.path)
		if !ok || hit.path == definer {
			continue
		}
		if line := globUseLine(lines, name); line > 0 {
			c.addReexportSite(definer, module.name, name, hit.path, line, lines[line-1], files)
		}
	}
}

var rustDeclaresRe = regexp.MustCompile(`\buse\b|\b(struct|enum|type|trait|fn|const|static|mod)\s+$`)

// globUseLine finds a use of a name a glob import must be providing: the file mentions it
// and neither imports nor declares it itself. 0 means no such use.
func globUseLine(lines []string, name string) int {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	first := 0
	for i, text := range lines {
		code := text
		if at := strings.Index(code, "//"); at >= 0 {
			code = code[:at]
		}
		if !re.MatchString(code) {
			continue
		}
		if rustDeclaresRe.MatchString(code[:re.FindStringIndex(code)[0]]) {
			return 0
		}
		if first == 0 {
			first = i + 1
		}
	}
	return first
}

func (c *doneChecker) addReexportSite(definer, module, name, path string, line int, code string, files map[string]bool) {
	if path == definer {
		return
	}
	key := fmt.Sprintf("reexport:%s:%s:%d", name, path, line)
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	files[path] = true
	c.sites = append(c.sites, OpenSite{
		Symbol: name,
		Change: string(impact.DeclRemoved),
		File:   path,
		Line:   line,
		Code:   strings.TrimSpace(code),
		IsTest: isTestFilePath(path),
		Via:    fmt.Sprintf("%s no longer re-exports it", module),
	})
}

type grepHit struct {
	path string
	line int
	text string
}

// gitGrep searches the working tree's Rust files. A failure counts against the check:
// "exit 1" is no match, anything else is not an answer.
func (c *doneChecker) gitGrep(dir, pattern string) []grepHit {
	pathspec := "*.rs"
	if dir != "" && dir != "." {
		pathspec = dir + "/**/*.rs"
	}
	cmd := exec.Command("git", "grep", "-n", "-E", "-e", pattern, "--", pathspec)
	cmd.Dir = c.repoRoot
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			c.failed++
		}
		return nil
	}
	var hits []grepHit
	for _, raw := range bytes.Split(out, []byte("\n")) {
		parts := strings.SplitN(string(raw), ":", 3)
		if len(parts) != 3 {
			continue
		}
		line := 0
		if _, err := fmt.Sscanf(parts[1], "%d", &line); err == nil {
			hits = append(hits, grepHit{path: parts[0], line: line, text: parts[2]})
		}
	}
	return hits
}
