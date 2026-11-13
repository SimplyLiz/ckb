package query

import (
	"bytes"
	"cmp"
	"go/ast"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// A field's key is written under the keys of the fields that hold its struct:
// ServerConfig.Port tagged `toml:"port"`, held by Config.Server tagged `toml:"server"`,
// is `[server] port`. The parents are read off the Go source by text; a struct no
// field holds is taken to be decoded at the top of a file.

// keySegment is one key on the way from the top of a file to a field.
type keySegment struct {
	key  string
	fold bool // The format's default key: decoders match the field name in any case
	any  bool // A map's key: any name
}

// wirePathMode says how much is known about where a field's key is written.
type wirePathMode int

const (
	// wirePathUnknown: a parent couldn't be read, so only the leaf key is matched,
	// and what it finds rarely breaks.
	wirePathUnknown wirePathMode = iota
	// wirePathRoot: no field holds the struct, so it is taken to be a file's top. A
	// key there is a site; the same key nested elsewhere only matched by name.
	wirePathRoot
	// wirePathKnown: the fields holding the struct give the path; a key nested
	// elsewhere is another struct's. One at the top of a file only matched by name:
	// the struct may be decoded on its own too.
	wirePathKnown
)

// wirePaths are the key paths a struct's fields are written under, without the
// field's own key.
type wirePaths struct {
	mode  wirePathMode
	paths [][]keySegment
}

// Bounds on the walk up from a struct to the top: depth, and paths kept.
const (
	maxWirePathDepth = 6
	maxWirePaths     = 32
)

// goWirePaths finds where the fields of struct container, declared in package
// directory dir, are written in format.
func (c *doneChecker) goWirePaths(format, dir, container string) wirePaths {
	if container == "" {
		return wirePaths{}
	}
	key := format + ":" + dir + ":" + container
	if found, ok := c.wirePaths[key]; ok {
		return found
	}
	paths, ok, parents := c.walkWirePaths(format, dir, container, 0, map[string]bool{})
	found := wirePaths{}
	switch {
	case !ok:
	case parents:
		found = wirePaths{mode: wirePathKnown, paths: paths}
	default:
		found = wirePaths{mode: wirePathRoot, paths: paths}
	}
	c.wirePaths[key] = found
	return found
}

// walkWirePaths returns the paths a struct is written under, false when a parent
// can't be read, and whether any field holds the struct at all.
func (c *doneChecker) walkWirePaths(format, dir, typeName string, depth int, visiting map[string]bool) ([][]keySegment, bool, bool) {
	if depth > maxWirePathDepth {
		return nil, false, false
	}
	if visiting[dir+":"+typeName] {
		return nil, true, true // A struct holding itself: the path that goes round adds nothing
	}
	visiting[dir+":"+typeName] = true
	defer delete(visiting, dir+":"+typeName)

	fields, ok := c.goFieldsOfType(dir, typeName)
	if !ok {
		return nil, false, false
	}
	if len(fields) == 0 {
		return [][]keySegment{nil}, true, false
	}
	var paths [][]keySegment
	for _, f := range fields {
		segs, serialized := f.segments(format)
		if !serialized {
			continue
		}
		above, ok, _ := c.walkWirePaths(format, f.dir, f.container, depth+1, visiting)
		if !ok {
			return nil, false, true
		}
		for _, p := range above {
			paths = append(paths, append(append([]keySegment{}, p...), segs...))
			if len(paths) > maxWirePaths {
				return nil, false, true
			}
		}
	}
	return paths, true, true
}

// goParentField is a struct field whose type holds a struct.
type goParentField struct {
	container string // The struct the field is in
	dir       string // Its package directory
	name      string // Empty for an embedded field
	typeName  string
	mods      string // *, [], map[K] before the type
	tag       string
}

// segments are the keys a parent field adds in format; false if it isn't serialized.
func (f goParentField) segments(format string) ([]keySegment, bool) {
	name, opts := "", ""
	if m := goFormatTagRe[format].FindStringSubmatch(f.tag); m != nil {
		name, opts, _ = strings.Cut(m[1], ",")
		if name == "-" && opts == "" {
			return nil, false
		}
	}
	var segs []keySegment
	switch {
	case name != "":
		segs = append(segs, keySegment{key: name})
	case format == "yaml" && strings.Contains(","+opts+",", ",inline,"):
		// Inlined: its keys are the parent's
	case f.name == "" && format != "yaml":
		// json and toml flatten an untagged embedded struct
	case f.name == "":
		segs = append(segs, keySegment{key: strings.ToLower(f.typeName)})
	case format == "yaml":
		segs = append(segs, keySegment{key: strings.ToLower(f.name)})
	default:
		segs = append(segs, keySegment{key: f.name, fold: true})
	}
	if strings.Contains(f.mods, "map[") {
		segs = append(segs, keySegment{any: true})
	}
	return segs, true
}

var (
	goTypeOpenerRe  = regexp.MustCompile(`^\s*type\s+(\w+)(?:\[[^\]]*\])?\s+struct\s*\{`)
	goGroupOpenerRe = regexp.MustCompile(`^\t(\w+)(?:\[[^\]]*\])?\s+struct\s*\{`)
	goAnyOpenerRe   = regexp.MustCompile(`\bstruct\s*\{\s*(?://.*)?$`)
	goTypeGroupRe   = regexp.MustCompile(`^type\s*\(`)
	goFormatTagRe   = map[string]*regexp.Regexp{
		"json": regexp.MustCompile(`\bjson:"([^"]*)"`),
		"yaml": regexp.MustCompile(`\byaml:"([^"]*)"`),
		"toml": regexp.MustCompile(`\btoml:"([^"]*)"`),
	}
)

// goFieldsOfType finds the exported struct fields of the tracked Go files whose type
// is typeName of package directory dir, through pointers, slices and maps; an
// unexported field isn't serialized. False means a field's struct couldn't be told:
// an anonymous struct, say, or a failed search. An empty dir matches any package.
func (c *doneChecker) goFieldsOfType(dir, typeName string) ([]goParentField, bool) {
	fieldRe := regexp.MustCompile(`^\s*(?:(\w+)\s+)?((?:\*|\[\d*\]|map\[[^\]]*\])*)(?:(\w+)\.)?` + regexp.QuoteMeta(typeName) +
		"\\s*(`[^`]*`)?\\s*(?://.*)?$")
	args := []string{"grep", "-n", "-w", "-F", "-e", typeName}
	if c.staged {
		args = append(args, "--cached")
	}
	cmd := exec.Command("git", append(args, "--", "*.go")...) // #nosec G204 //nolint:gosec // fixed git subcommand, the name is an argument
	cmd.Dir = c.repoRoot
	out, err := cmd.Output()
	if err != nil {
		if exit, isExit := err.(*exec.ExitError); isExit && exit.ExitCode() == 1 {
			return nil, true // No match
		}
		return nil, false
	}
	var fields []goParentField
	files := make(map[string][]string)
	for _, raw := range bytes.Split(out, []byte("\n")) {
		parts := strings.SplitN(string(raw), ":", 3)
		if len(parts) != 3 || ignoredByBuild(parts[0]) {
			continue
		}
		m := fieldRe.FindStringSubmatch(parts[2])
		if m == nil || (m[1] != "" && !ast.IsExported(m[1])) {
			continue
		}
		line := 0
		for _, ch := range parts[1] {
			line = line*10 + int(ch-'0')
		}
		lines, read := files[parts[0]]
		if !read {
			lines = c.goSource(parts[0])
			files[parts[0]] = lines
		}
		if dir != "" && !goNamesPackage(lines, path.Dir(parts[0]), m[3], dir) {
			continue // Another package's type of the same name
		}
		container, inStruct, known := goEnclosingStruct(lines, line-1)
		if !inStruct {
			continue // A variable, a parameter: not a field
		}
		if !known {
			return nil, false
		}
		fields = append(fields, goParentField{container: container, dir: path.Dir(parts[0]), name: m[1], typeName: typeName, mods: m[2], tag: m[4]})
	}
	return fields, true
}

// goNamesPackage says whether a type spelled with qualifier in a file of package
// directory fileDir is the one of package directory dir: unqualified in the same
// directory, or qualified by an import of it.
func goNamesPackage(lines []string, fileDir, qualifier, dir string) bool {
	if qualifier == "" {
		return fileDir == dir
	}
	if dir == "." {
		return false // A module's root package: its import path isn't known here
	}
	for _, line := range lines {
		m := goImportRe.FindStringSubmatch(line)
		if m == nil || !(m[2] == dir || strings.HasSuffix(m[2], "/"+dir)) {
			continue
		}
		if name := cmp.Or(m[1], path.Base(m[2])); name == qualifier {
			return true
		}
	}
	return false
}

var goImportRe = regexp.MustCompile(`^\s*(?:import\s+)?(?:(\w+)\s+)?"([^"]+)"\s*(?://.*)?$`)

// goSource reads a tracked Go file's current side.
func (c *doneChecker) goSource(path string) []string {
	if c.staged {
		return c.gitShow(":" + path)
	}
	data, err := os.ReadFile(filepath.Join(c.repoRoot, path)) // #nosec G304 -- a tracked file of the repo
	if err != nil {
		return nil
	}
	return splitFileLines(string(data))
}

// goEnclosingStruct names the struct type whose body holds line (0-based): inStruct
// says the nearest open brace is a struct's, known that its type could be named.
func goEnclosingStruct(lines []string, line int) (name string, inStruct, known bool) {
	if line < 0 || line >= len(lines) {
		return "", false, false
	}
	depth := 0
	for i := line - 1; i >= 0; i-- {
		text := lines[i]
		if j := strings.Index(text, "//"); j >= 0 {
			text = text[:j]
		}
		depth += strings.Count(text, "}") - strings.Count(text, "{")
		if depth >= 0 {
			continue
		}
		if m := goTypeOpenerRe.FindStringSubmatch(text); m != nil {
			return m[1], true, true
		}
		if !goAnyOpenerRe.MatchString(text) {
			return "", false, false
		}
		// A member of a `type (` group, at one tab, or an anonymous struct's field.
		if m := goGroupOpenerRe.FindStringSubmatch(text); m != nil {
			for k := i - 1; k >= 0; k-- {
				if above := lines[k]; above != "" && above[0] != '\t' && above[0] != ' ' {
					if goTypeGroupRe.MatchString(above) {
						return m[1], true, true
					}
					break
				}
			}
		}
		return "", true, false
	}
	return "", false, false
}

// configKey is a key a config file writes: its line (1-based) and the keys from the
// top of the file down to it.
type configKey struct {
	line int
	path []string
}

// configKeys reads the keys a config file writes, with their paths. Text, not a
// parser: inline TOML tables and YAML flow mappings aren't followed.
func configKeys(format string, lines []string) []configKey {
	switch format {
	case "toml":
		return tomlKeys(lines)
	case "yaml":
		return yamlKeys(lines)
	case "json":
		return jsonKeys(lines)
	}
	return nil
}

var (
	tomlTableRe = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(?:#.*)?$`)
	tomlKeyRe   = regexp.MustCompile(`^\s*((?:[A-Za-z0-9_-]+|"[^"]*"|'[^']*')(?:\s*\.\s*(?:[A-Za-z0-9_-]+|"[^"]*"|'[^']*'))*)\s*=`)
	tomlPartRe  = regexp.MustCompile(`[A-Za-z0-9_-]+|"[^"]*"|'[^']*'`)
)

// tomlDotted splits a dotted TOML key into its parts, unquoted.
func tomlDotted(key string) []string {
	var parts []string
	for _, p := range tomlPartRe.FindAllString(key, -1) {
		parts = append(parts, strings.Trim(p, `"'`))
	}
	return parts
}

func tomlKeys(lines []string) []configKey {
	var keys []configKey
	var table []string
	inString := ""
	for i, line := range lines {
		if inString != "" {
			if strings.Count(line, inString)%2 == 1 {
				inString = ""
			}
			continue
		}
		if m := tomlTableRe.FindStringSubmatch(line); m != nil && !strings.Contains(line, "=") {
			table = tomlDotted(m[1])
			continue
		}
		m := tomlKeyRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		keys = append(keys, configKey{line: i + 1, path: append(append([]string{}, table...), tomlDotted(m[1])...)})
		for _, quote := range []string{`"""`, `'''`} {
			if strings.Count(line, quote)%2 == 1 {
				inString = quote
			}
		}
	}
	return keys
}

var yamlKeyRe = regexp.MustCompile(`^(\s*)((?:-\s+)*)(?:([^\s"'#:\-{\[][^:#]*?|"[^"]*"|'[^']*'))\s*:(?:\s|$)`)

func yamlKeys(lines []string) []configKey {
	type level struct {
		indent int
		key    string
	}
	var keys []configKey
	var stack []level
	block := -1 // The indent of a key whose value is a block scalar
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if block >= 0 {
			if trimmed == "" || indent > block {
				continue
			}
			block = -1
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "---" || trimmed == "..." {
			stack = nil
			continue
		}
		m := yamlKeyRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		at := len(m[1]) + len(m[2])
		key := strings.Trim(strings.TrimSpace(m[3]), `"'`)
		for len(stack) > 0 && stack[len(stack)-1].indent >= at {
			stack = stack[:len(stack)-1]
		}
		path := make([]string, 0, len(stack)+1)
		for _, l := range stack {
			path = append(path, l.key)
		}
		keys = append(keys, configKey{line: i + 1, path: append(path, key)})
		stack = append(stack, level{indent: at, key: key})
		value := strings.TrimSpace(line[len(m[0]):])
		if strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") {
			block = at
		}
	}
	return keys
}

func jsonKeys(lines []string) []configKey {
	type frame struct {
		object bool
		key    string
	}
	content := strings.Join(lines, "\n")
	var keys []configKey
	var stack []frame
	line := 1
	for i := 0; i < len(content); i++ {
		switch ch := content[i]; ch {
		case '\n':
			line++
		case '{', '[':
			stack = append(stack, frame{object: ch == '{'})
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case '"':
			start, at := i+1, line
			for i++; i < len(content) && content[i] != '"'; i++ {
				if content[i] == '\\' {
					i++
				} else if content[i] == '\n' {
					line++
				}
			}
			if i >= len(content) {
				return keys
			}
			s := content[start:i]
			j := i + 1
			for j < len(content) && strings.ContainsRune(" \t\r\n", rune(content[j])) {
				j++
			}
			if j >= len(content) || content[j] != ':' || len(stack) == 0 || !stack[len(stack)-1].object {
				continue
			}
			var path []string
			for _, f := range stack[:len(stack)-1] {
				if f.object {
					path = append(path, f.key)
				}
			}
			keys = append(keys, configKey{line: at, path: append(path, s)})
			stack[len(stack)-1].key = s
		}
	}
	return keys
}

// keyHit is a line writing a changed key; rarely when only its name matched.
type keyHit struct {
	line   int
	rarely bool
}

// changedKeyLines finds the lines of a config file writing old where the field's
// struct puts it. Where that isn't known, every line writing the key matches, as
// rarely breaking.
func changedKeyLines(format string, lines []string, old string, where wirePaths) []keyHit {
	var hits []keyHit
	for _, k := range configKeys(format, lines) {
		if k.path[len(k.path)-1] != old {
			continue
		}
		parent := k.path[:len(k.path)-1]
		switch where.mode {
		case wirePathUnknown:
			hits = append(hits, keyHit{line: k.line, rarely: true})
		case wirePathRoot:
			hits = append(hits, keyHit{line: k.line, rarely: len(parent) > 0})
		case wirePathKnown:
			matched := false
			for _, p := range where.paths {
				if pathMatches(p, parent) {
					matched = true
					break
				}
			}
			switch {
			case matched:
				hits = append(hits, keyHit{line: k.line})
			case len(parent) == 0:
				// The struct may also be decoded on its own, as a file's top
				hits = append(hits, keyHit{line: k.line, rarely: true})
			}
		}
	}
	return hits
}

func pathMatches(want []keySegment, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for i, seg := range want {
		switch {
		case seg.any:
		case seg.fold && strings.EqualFold(seg.key, got[i]):
		case seg.key != got[i]:
			return false
		}
	}
	return true
}
