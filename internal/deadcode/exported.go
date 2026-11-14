package deadcode

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// ExportRules decides whether a symbol is part of its module's interface,
// which is what makes an unreachable symbol "unwired" rather than plain dead
// code. Only Go spells that in the name. scip-typescript and rust-analyzer
// record nothing about `export` or `pub` — an exported and a module-private
// function get the same kind, display name and docs — so for those the
// definition line in the source decides.
//
// What counts is what the compiler leaves unchecked: an unused private Rust
// fn or pub(crate) item gets a dead_code warning, an unused non-exported TS
// function gets noUnusedLocals, so neither is counted here. A bare `pub` in a
// lib and an `export` get no warning from anyone.
type ExportRules struct {
	repoRoot string
	index    *scip.SCIPIndex
	files    map[string]*sourceFile
}

type sourceFile struct {
	lines []string        // nil when the file can't be read
	named map[string]bool // TS/JS: names exported by a list or assignment elsewhere in the file
}

func NewExportRules(repoRoot string, index *scip.SCIPIndex) *ExportRules {
	return &ExportRules{repoRoot: repoRoot, index: index, files: make(map[string]*sourceFile)}
}

// site returns the file and 1-based line of the symbol's definition, or
// ("", 0) when the index has no definition occurrence for it.
func (r *ExportRules) Site(symbolID string) (string, int) {
	if r.index == nil || r.index.DefinitionIndex == nil {
		return "", 0
	}
	ref := r.index.DefinitionIndex[symbolID]
	if ref == nil || ref.Doc == nil || ref.Occ == nil || len(ref.Occ.Range) == 0 {
		return "", 0
	}
	return ref.Doc.RelativePath, int(ref.Occ.Range[0]) + 1
}

// line returns the source text of the symbol's definition line and the file
// it is in; ok is false when either is unknown.
func (r *ExportRules) line(symbolID string) (text string, file *sourceFile, ok bool) {
	path, n := r.Site(symbolID)
	if path == "" {
		return "", nil, false
	}
	f := r.file(path)
	if n < 1 || n > len(f.lines) {
		return "", f, false
	}
	return f.lines[n-1], f, true
}

func (r *ExportRules) file(path string) *sourceFile {
	if f, ok := r.files[path]; ok {
		return f
	}
	f := &sourceFile{}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(r.repoRoot, path)
	}
	if data, err := os.ReadFile(full); err == nil {
		text := string(data)
		f.lines = strings.Split(text, "\n")
		f.named = tsNamedExports(text)
	}
	r.files[path] = f
	return f
}

// exported reports whether the symbol is part of its module's interface.
func (r *ExportRules) Exported(symbolID, name string) bool {
	switch scheme, _, _ := strings.Cut(symbolID, " "); scheme {
	case "scip-typescript", "scip-javascript":
		return r.tsExported(symbolID, name)
	case "scip-python":
		return pythonExported(symbolID, name)
	case "rust-analyzer":
		text, _, ok := r.line(symbolID)
		return ok && rustPubRe.MatchString(text)
	default:
		return startsUpper(name)
	}
}

var (
	// `export function`, `export const`, `export default class`, `export abstract class` …
	tsExportDeclRe = regexp.MustCompile(`^\s*export\b`)
	// `export { a, b as c }` without a `from`: that would export another file's symbols.
	tsExportListRe = regexp.MustCompile(`(?s)\bexport\s*\{([^}]*)\}\s*(;|\n|$)`)
	// `export default a;`
	tsExportDefaultRe = regexp.MustCompile(`\bexport\s+default\s+([A-Za-z_$][\w$]*)\s*;?\s*(\n|$)`)
	// CommonJS: `exports.a =`, `module.exports.a =`, `module.exports = { a, b: c }`
	cjsExportPropRe  = regexp.MustCompile(`\b(?:module\.)?exports\.([A-Za-z_$][\w$]*)\s*=`)
	cjsExportObjRe   = regexp.MustCompile(`(?s)\bmodule\.exports\s*=\s*\{([^}]*)\}`)
	tsMemberHiddenRe = regexp.MustCompile(`^\s*(?:(?:static|readonly|override|abstract|async|declare)\s+)*(?:private|protected)\b`)

	// A bare `pub`; `pub(crate)`, `pub(super)` and `pub(in …)` are the crate's own.
	rustPubRe = regexp.MustCompile(`^\s*(?:#\[[^\]]*\]\s*)*pub\s`)
)

func tsNamedExports(text string) map[string]bool {
	named := make(map[string]bool)
	for _, m := range tsExportListRe.FindAllStringSubmatch(text, -1) {
		for _, entry := range strings.Split(m[1], ",") {
			local, _, _ := strings.Cut(strings.TrimSpace(entry), " as ")
			local = strings.TrimPrefix(strings.TrimSpace(local), "type ")
			if local = strings.TrimSpace(local); local != "" {
				named[local] = true
			}
		}
	}
	for _, m := range tsExportDefaultRe.FindAllStringSubmatch(text, -1) {
		named[m[1]] = true
	}
	for _, m := range cjsExportPropRe.FindAllStringSubmatch(text, -1) {
		named[m[1]] = true
	}
	for _, m := range cjsExportObjRe.FindAllStringSubmatch(text, -1) {
		for _, entry := range strings.Split(m[1], ",") {
			// `a`, `a: b` and `a() {}` all export a; with `a: b` the local is b.
			key, value, hasValue := strings.Cut(strings.TrimSpace(entry), ":")
			local := strings.TrimSpace(key)
			if hasValue {
				local = strings.TrimSpace(value)
			}
			if i := strings.IndexAny(local, "( "); i >= 0 {
				local = local[:i]
			}
			if local != "" {
				named[local] = true
			}
		}
	}
	return named
}

func (r *ExportRules) tsExported(symbolID, name string) bool {
	if owner := tsOwner(symbolID); owner != "" {
		// A member is reachable from outside as far as its class is, unless
		// it hides itself.
		member := strings.Trim(symbolID[len(owner):], "`")
		if strings.HasPrefix(member, "#") || !r.tsExported(owner, ExtractName(owner)) {
			return false
		}
		text, _, ok := r.line(symbolID)
		return !ok || !tsMemberHiddenRe.MatchString(text)
	}
	text, file, ok := r.line(symbolID)
	if ok && tsExportDeclRe.MatchString(text) {
		return true
	}
	return file != nil && file.named[name]
}

// tsOwner returns the class or interface a member belongs to
// ("…/`lib.ts`/Box#open()." → "…/`lib.ts`/Box#"), or "" for a top-level symbol.
func tsOwner(symbolID string) string {
	i := strings.LastIndex(symbolID, "#")
	if i < 0 || i == len(symbolID)-1 {
		return ""
	}
	return symbolID[:i+1]
}

// pythonExported follows the underscore convention for the name and every
// class or function it is nested in. Modules are left out: `_impl.py` is
// commonly re-exported from a package's __init__.
func pythonExported(symbolID, name string) bool {
	if strings.HasPrefix(name, "_") {
		return false
	}
	sym, err := bindingscip.ParseSymbol(symbolID)
	if err != nil {
		return true
	}
	for _, d := range sym.Descriptors {
		if d.Suffix != bindingscip.Descriptor_Namespace && strings.HasPrefix(d.Name, "_") {
			return false
		}
	}
	return true
}

// ExtractName returns the last descriptor of a SCIP symbol ID without its
// suffix: "…/`lib.ts`/Box#" → "Box", "…/run()." → "run".
func ExtractName(symbolID string) string {
	parts := strings.Split(symbolID, "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	last = strings.TrimSuffix(last, "().")
	last = strings.TrimSuffix(last, "#")
	last = strings.TrimSuffix(last, ".")
	return last
}

func startsUpper(name string) bool {
	for _, r := range name {
		return unicode.IsUpper(r)
	}
	return false
}
