package diff

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// rustImplMethodRe reads a method's impl from a rust-analyzer symbol: the type in the
// first brackets, the trait, for a trait impl, in the second, then the method.
var rustImplMethodRe = regexp.MustCompile(`(?:^|[/ ])impl#\[([^\]]+)\](?:\[([^\]]+)\])?([A-Za-z_]\w*)\(\)\.$`)

// rustImplHeaderRe reads an impl header: `impl<T> Trait<T> for Type<T> {` or `impl Type {`.
var rustImplHeaderRe = regexp.MustCompile(`^\s*(?:unsafe\s+)?impl\b(?:\s*<.*?>)?\s+(?:(?:dyn\s+)?([\w:]+)(?:<.*?>)?\s+for\s+)?&?(?:mut\s+)?([\w:]+)`)

// rustInherentImplRe is the header of an impl of no trait. It declares nothing its
// items don't: like a brace, it is structure. A trait impl's header is not, since
// removing it takes the trait away from the type.
var rustInherentImplRe = regexp.MustCompile(`^\s*(?:unsafe\s+)?impl\b[^{]*\{?\s*$`)

func isRustInherentImplHeader(line string) bool {
	return rustInherentImplRe.MatchString(line) && !containsWord(line, "for") && !strings.Contains(line, ";")
}

// resolveRustMoves finds where a Rust method the diff removed went, if its impl block
// moved to another file of the same crate. rust-analyzer names a method by the module
// of its impl, but callers reach it through the type, so for them nothing was removed.
// A trait's method is as visible as the trait. An inherent method that isn't pub or
// pub(crate) is visible by module, and moving it to another one is a change of its own:
// it stays removed.
func resolveRustMoves(texts *diffTexts, result []impact.ChangedSymbol) {
	for i := range result {
		sym := &result[i]
		if sym.Declaration != impact.DeclRemoved || sym.DeclarationBefore == "" || declarationLanguage(sym.File) != "rust" {
			continue
		}
		m := rustImplMethodRe.FindStringSubmatch(sym.SymbolID)
		if m == nil {
			continue
		}
		typ, trait, method := rustPathTail(m[1]), rustPathTail(m[2]), m[3]
		if trait == "" && !rustCrateVisible(sym.DeclarationBefore) {
			continue
		}
		root, ok := rustCrateRoot(sym.File)
		if !ok {
			continue
		}
		for _, file := range texts.files {
			if file.Deleted || file.NewPath == "" || file.NewPath == sym.File || !strings.HasSuffix(file.NewPath, ".rs") {
				continue
			}
			if other, ok := rustCrateRoot(file.NewPath); !ok || other != root {
				continue
			}
			text := texts.read(file)
			line := findDeclaration(text.newAll, text.changedNew, sym.DeclarationBefore, method, "rust")
			if line == 0 {
				continue
			}
			implTrait, implType, inImpl := rustEnclosingImpl(text.newAll, line)
			if !inImpl || implType != typ || implTrait != trait {
				continue
			}
			decl := readDeclaration(text.newAll, line, "rust")
			if trait == "" && !rustCrateVisible(decl.text) {
				continue
			}
			// A new file is in the crate once a `mod` declares it. Where the parent
			// module can be read and has no such line, the impl isn't compiled.
			if declared, known := rustModuleDeclared(texts, file); known && !declared {
				sym.Note = "an impl with this method is in " + file.NewPath + ", which no `mod` line declares"
				continue
			}
			sym.ChangeType = impact.ChangeModified
			sym.DeclarationAfter = decl.text
			sym.Declaration = ""
			if decl.complete {
				sym.Declaration = compareDeclarations(sym.DeclarationBefore, decl.text, method, "rust")
			}
			sym.Note = "impl moved to " + file.NewPath
			break
		}
	}
}

// rustPathTail is the last segment of a path, without generics: Point for a::Point<T>.
func rustPathTail(path string) string {
	if at := strings.IndexByte(path, '<'); at >= 0 {
		path = path[:at]
	}
	if at := strings.LastIndex(path, "::"); at >= 0 {
		path = path[at+2:]
	}
	return strings.TrimSpace(path)
}

// rustCrateVisible says whether a declaration is visible anywhere in its crate, whatever
// module it sits in: pub, or pub(crate).
func rustCrateVisible(declaration string) bool {
	text := strings.TrimSpace(declaration)
	if strings.HasPrefix(text, "pub ") {
		return true
	}
	if !strings.HasPrefix(text, "pub(") {
		return false
	}
	end := strings.IndexByte(text, ')')
	return end > 0 && strings.TrimSpace(text[len("pub("):end]) == "crate"
}

// rustCrateRoot is the directory a file's library crate is rooted in: the part of its
// path before src/. A file outside src/, in tests/ or examples/, and one in src/bin/,
// is a crate of its own.
func rustCrateRoot(path string) (string, bool) {
	root, rest, ok := "", "", false
	switch {
	case strings.HasPrefix(path, "src/"):
		rest, ok = path[len("src/"):], true
	case strings.Contains(path, "/src/"):
		at := strings.LastIndex(path, "/src/")
		root, rest, ok = path[:at], path[at+len("/src/"):], true
	}
	if !ok || strings.HasPrefix(rest, "bin/") {
		return "", false
	}
	return root, true
}

// rustEnclosingImpl finds the impl block a line sits in, by indentation as rustfmt
// writes it: the nearest line above that is indented less. False if that isn't an impl.
func rustEnclosingImpl(lines map[int]string, line int) (trait, typ string, ok bool) {
	indent := leadingSpace(lines[line])
	if indent == 0 {
		return "", "", false
	}
	for current := line - 1; current >= 1; current-- {
		text, shown := lines[current]
		if !shown {
			return "", "", false
		}
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#[") || leadingSpace(text) >= indent {
			continue
		}
		m := rustImplHeaderRe.FindStringSubmatch(text)
		if m == nil {
			return "", "", false
		}
		return rustPathTail(m[1]), rustPathTail(m[2]), true
	}
	return "", "", false
}

func leadingSpace(text string) int {
	return len(text) - len(strings.TrimLeft(text, " \t"))
}

// rustModuleParents returns the module name a Rust file stands for and the files that
// can declare it: for src/a/b.rs, b in src/a.rs or src/a/mod.rs; at the top, the crate
// roots.
func rustModuleParents(path string) (name string, parents []string) {
	dir, base := filepath.ToSlash(filepath.Dir(path)), strings.TrimSuffix(filepath.Base(path), ".rs")
	if base == "mod" {
		dir, base = filepath.ToSlash(filepath.Dir(dir)), filepath.Base(dir)
	}
	if dir == "src" || strings.HasSuffix(dir, "/src") {
		return base, []string{dir + "/lib.rs", dir + "/main.rs"}
	}
	return base, []string{dir + ".rs", dir + "/mod.rs"}
}

// rustModuleDeclared says whether a `mod` line declares a file the diff adds. known is
// false if no parent module could be read whole: then the hunks may just not show it.
// A file that was there before is taken as declared.
func rustModuleDeclared(texts *diffTexts, file *impact.ChangedFile) (declared, known bool) {
	if !file.IsNew {
		return true, true
	}
	name, parents := rustModuleParents(file.NewPath)
	modLine := regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+` + regexp.QuoteMeta(name) + `\s*;`)
	has := func(lines map[int]string) bool {
		for _, text := range lines {
			if modLine.MatchString(text) {
				return true
			}
		}
		return false
	}
	for _, parent := range parents {
		if changed := texts.byPath[parent]; changed != nil && !changed.Deleted {
			if has(texts.read(changed).newAll) {
				return true, true
			}
			if sides := texts.m.wholeFile(changed); sides != nil && sides.new != nil {
				known = true
			}
			continue
		}
		if texts.m.files == nil {
			continue
		}
		if lines := texts.m.files(parent, false); lines != nil {
			known = true
			for _, text := range lines {
				if modLine.MatchString(text) {
					return true, true
				}
			}
		}
	}
	return false, known
}
