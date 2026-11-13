package unwired

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/architecture"
)

// A bundler's entry is a program of its own: a worker esbuild builds from
// src/render/main.ts runs, though nothing in the app imports it. The entry is
// written in the build, not in the code: esbuild's entryPoints, tsup's and
// webpack's entry, rollup's and vite's input, in a script package.json runs or
// in a bundler config at the root, or as an argument on an esbuild or tsup
// command line in package.json itself.

// bundlerConfigs are the root config files read for entries.
var bundlerConfigs = []string{"esbuild", "tsup", "rollup", "vite", "webpack"}

// scriptExts are the extensions of a file a script runs or a bundler reads.
var scriptExts = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}

// entryKey finds where an entry is given: `entryPoints: …`, `entry: …`,
// `input: …`, quoted or not, or `const entryPoints = …`.
var entryKey = regexp.MustCompile(`(?:\b|["'])(entryPoints|entry|input)["']?\s*(:|=[^=>])`)

// stringLiteral matches a quoted string without interpolation.
var stringLiteral = regexp.MustCompile("\"([^\"\\\\\\n]*)\"|'([^'\\\\\\n]*)'|`([^`$\\\\]*)`")

// buildEntrypoints returns the files the package.json scripts and the root
// bundler configs name as bundle entries, repo-relative and existing.
func (d *Detector) buildEntrypoints(scripts map[string]string) []architecture.Entrypoint {
	seen := make(map[string]bool)
	var out []architecture.Entrypoint
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, architecture.Entrypoint{FileId: path, Name: filepath.Base(path), Kind: architecture.EntrypointServer})
	}

	var files []string
	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entries, read := commandEntries(scripts[name])
		for _, e := range entries {
			add(d.resolve(e, ""))
		}
		files = append(files, read...)
	}
	for _, tool := range bundlerConfigs {
		for _, ext := range scriptExts {
			files = append(files, tool+".config"+ext)
		}
	}

	read := make(map[string]bool)
	for _, f := range files {
		rel := d.resolve(f, "")
		if rel == "" || read[rel] {
			continue
		}
		read[rel] = true
		content, err := os.ReadFile(filepath.Join(d.repoRoot, rel)) // #nosec G304 -- a file of the repo
		if err != nil {
			continue
		}
		for _, e := range entryStrings(string(content)) {
			add(d.resolve(e, filepath.Dir(rel)))
		}
	}
	return out
}

// commandEntries reads a package.json script: the files an esbuild or tsup
// call takes as entries or a --entry flag names, and the script files it runs,
// which may call a bundler themselves.
func commandEntries(cmd string) (entries, files []string) {
	for _, segment := range regexp.MustCompile(`&&|\|\||[;|]`).Split(cmd, -1) {
		fields := strings.Fields(segment)
		bundler := false
		for i := 0; i < len(fields); i++ {
			tok := strings.Trim(fields[i], `"'`)
			switch base := filepath.Base(tok); {
			case base == "esbuild" || base == "tsup":
				bundler = true
				continue
			case tok == "--entry" && i+1 < len(fields):
				entries = append(entries, strings.Trim(fields[i+1], `"'`))
				i++
				continue
			case strings.HasPrefix(tok, "--entry="):
				entries = append(entries, strings.TrimPrefix(tok, "--entry="))
				continue
			}
			if strings.HasPrefix(tok, "-") || !hasScriptExt(tok) {
				continue
			}
			if bundler {
				entries = append(entries, tok)
			} else {
				files = append(files, tok)
			}
		}
	}
	return entries, files
}

// entryStrings returns the file names in the values of a script's entry keys:
// a string, or the strings in an array or object, `join(root, "src/a.ts")`
// included.
func entryStrings(content string) []string {
	var out []string
	for _, m := range entryKey.FindAllStringSubmatchIndex(content, -1) {
		// `=` takes the next character with it, so the value starts one back.
		value := entryValue(content[m[1]-1:])
		for _, s := range stringLiteral.FindAllStringSubmatch(value, -1) {
			lit := s[1] + s[2] + s[3]
			if hasScriptExt(lit) {
				out = append(out, lit)
			}
		}
	}
	return out
}

// entryValue cuts the value that starts after `:` or `=`: up to the matching
// bracket when it opens one, else up to the end of the expression.
func entryValue(s string) string {
	s = strings.TrimLeft(s, ":= \t\r\n")
	if s == "" {
		return ""
	}
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '[', '{', '(':
			depth++
		case ']', '}', ')':
			if depth == 0 {
				return s[:i]
			}
			depth--
			if depth == 0 && (s[0] == '[' || s[0] == '{') {
				return s[:i+1]
			}
		case ',', ';', '\n':
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}

func hasScriptExt(path string) bool {
	ext := filepath.Ext(path)
	for _, e := range scriptExts {
		if ext == e {
			return true
		}
	}
	return false
}

// resolve turns a name from a script into a repo-relative path of an existing
// file: relative to the repo root, then to dir, the directory of the script
// that names it. A path outside the repo or under node_modules is no entry.
func (d *Detector) resolve(name, dir string) string {
	if name == "" || strings.Contains(name, "node_modules") {
		return ""
	}
	name = filepath.FromSlash(name)
	candidates := []string{name}
	if dir != "" && dir != "." {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	for _, c := range candidates {
		if filepath.IsAbs(c) {
			continue
		}
		rel := filepath.Clean(c)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if info, err := os.Stat(filepath.Join(d.repoRoot, rel)); err == nil && !info.IsDir() {
			return filepath.ToSlash(rel)
		}
	}
	return ""
}
