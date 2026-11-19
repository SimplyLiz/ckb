package query

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// A Go field's serialized key is a contract no compiler checks: the config file that
// still writes `port = 8080` after the tag became `toml:"listen_port"` is silently
// ignored. These look for the old key in the repo's own config files.
//
// The key is matched where the field's struct puts it, under the keys of the fields
// holding the struct (donecheck_config_paths.go). Which file decodes into which root
// isn't known: a root struct's keys are matched at the top of any file of the format
// (see docs/backlog/string-contracts-and-config-impact.md).

// wireKeyChange is a serialized key a field's change renamed or removed.
type wireKeyChange struct {
	format string // json, yaml, toml
	old    string
	now    string // "" when gone
}

// goWireKeyChanges lists the keys a Go field's change renamed or removed. A tag that
// went away entirely leaves the format's default key, which a case-insensitive
// decoder may still match to the old one: that is not listed.
func goWireKeyChanges(before, after string, removed bool) []wireKeyChange {
	was, now := goWireKeys(before), goWireKeys(after)
	var changes []wireKeyChange
	for _, format := range []string{"json", "yaml", "toml"} {
		old := was[format]
		if old == "" {
			continue
		}
		current, still := now[format]
		switch {
		case removed:
			changes = append(changes, wireKeyChange{format: format, old: old})
		case still && current != "" && current != old:
			changes = append(changes, wireKeyChange{format: format, old: old, now: current})
		}
	}
	return changes
}

// goWireKeys reads the keys a declaration's struct tag gives a field, by format.
func goWireKeys(decl string) map[string]string {
	found := make(map[string]string)
	if !strings.Contains(decl, "`") {
		return found
	}
	for _, m := range goWireTagRe.FindAllStringSubmatch(decl[strings.Index(decl, "`"):], -1) {
		if m[2] != "-" {
			found[m[1]] = m[2]
		}
	}
	return found
}

// goTagOnlyChange says whether two declarations of a Go field differ in nothing but
// the struct tag (and comments): the name and type the Go code uses are the same.
func goTagOnlyChange(before, after string) bool {
	if !strings.Contains(before, "`") && !strings.Contains(after, "`") {
		return false
	}
	strip := func(decl string) string {
		if i := strings.Index(decl, "//"); i >= 0 && !strings.Contains(decl[i:], "`") {
			decl = decl[:i]
		}
		decl = goStructTagRe.ReplaceAllString(decl, "")
		return strings.Join(strings.Fields(decl), " ")
	}
	was, now := strip(before), strip(after)
	return was != "" && was == now
}

// goStructTagRe is a field's raw-string struct tag.
var goStructTagRe = regexp.MustCompile("`[^`]*`")

// configFormat is the serialization a tracked file is written in, or "".
func configFormat(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		return "toml"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	}
	return ""
}

// skipConfigFile leaves out files no program of the repo reads as its config:
// dependencies and lockfiles.
func skipConfigFile(path string) bool {
	switch filepath.Base(path) {
	case "package-lock.json", "composer.lock", "Cargo.lock", "poetry.lock":
		return true
	}
	return ignoredByBuild(path) && !strings.Contains("/"+filepath.ToSlash(path), "/testdata/")
}

// maxConfigSites caps the lines listed per key: a leaf key as common as "name" hits
// every manifest, and the first ones say what there is to look at.
const maxConfigSites = 20

// useLeafKey is a config line writing a changed key whose path from the top of the
// file couldn't be matched to the field's struct: only the key's name is the same.
// It is counted with the uses that rarely break, not listed.
const useLeafKey = "leaf-key"

// checkConfigKeys lists the lines of the repo's tracked config files that still
// write a renamed or removed key, where the field's struct puts it: under the keys
// of the fields holding the struct. A file whose old key the diff took out counts
// as updated. It returns the files with a line left open.
func (c *doneChecker) checkConfigKeys(sym impact.ChangedSymbol, changes []wireKeyChange) []string {
	if len(changes) == 0 {
		return nil
	}
	out, err := exec.Command("git", "-C", c.repoRoot, "ls-files", "-z").Output() // #nosec G204 //nolint:gosec // fixed git subcommand
	if err != nil {
		return nil
	}
	container := ""
	if id, err := scip.ParseSCIPIdentifier(sym.SymbolID); err == nil {
		container = id.GetContainerName()
	}
	dir := "" // The struct's package directory; unknown matches any
	if sym.File != "" {
		dir = filepath.ToSlash(filepath.Dir(sym.File))
	}
	where := make(map[string]wirePaths)
	var open []string
	for _, path := range strings.Split(string(out), "\x00") {
		format := configFormat(path)
		if format == "" || skipConfigFile(path) {
			continue
		}
		if _, ok := where[format]; !ok {
			where[format] = c.goWirePaths(format, dir, container)
		}
		newLines := c.readConfig(path, changes)
		if newLines == nil {
			// None of the old keys is left. Where the diff took one out, it was updated.
			if file := c.files[path]; file != nil && !file.Deleted {
				for _, change := range changes {
					if change.format == format && c.writesKey(file, path, change.old, where[format]) {
						c.updated++
					}
				}
			}
			continue
		}
		listed := false
		for _, change := range changes {
			if change.format != format {
				continue
			}
			hits := changedKeyLines(format, newLines, change.old, where[format])
			for n, hit := range hits {
				if n == maxConfigSites {
					break
				}
				via := fmt.Sprintf("%s key %q gone", format, change.old)
				if change.now != "" {
					via = fmt.Sprintf("%s key %q -> %q", format, change.old, change.now)
				}
				key := fmt.Sprintf("config:%s:%s:%d", sym.Name, path, hit.line)
				if c.seen[key] {
					continue
				}
				c.seen[key] = true
				site := OpenSite{
					Symbol: sym.Name,
					Change: map[bool]string{true: "changed", false: "removed"}[change.now != ""],
					File:   path,
					Line:   hit.line,
					Code:   strings.TrimSpace(newLines[hit.line-1]),
					IsTest: isTestFilePath(path),
					Via:    via,
				}
				if hit.rarely {
					site.Use = useLeafKey
				}
				c.sites = append(c.sites, site)
				listed = true
			}
			if len(hits) == 0 && c.files[path] != nil && c.writesKey(c.files[path], path, change.old, where[format]) {
				c.updated++ // The diff took this key out; another one is still there
			}
		}
		if listed {
			open = append(open, path)
		}
	}
	return open
}

// maxConfigFileBytes skips generated data that happens to be JSON.
const maxConfigFileBytes = 1 << 20

// readConfig reads a config file's current side, or nil when it is too big, can't
// be read or holds none of the keys. It doesn't spend the done check's file budget,
// which is for the files with a use: a repo of JSON fixtures would use it up.
func (c *doneChecker) readConfig(path string, changes []wireKeyChange) []string {
	if file := c.files[path]; file != nil {
		if file.Deleted {
			return nil
		}
		path = cmp.Or(file.NewPath, path)
	}
	var content string
	if c.staged {
		content = strings.Join(c.gitShow(":"+path), "\n")
	} else {
		full := filepath.Join(c.repoRoot, path)
		if info, err := os.Stat(full); err != nil || info.Size() > maxConfigFileBytes {
			return nil
		}
		data, err := os.ReadFile(full) // #nosec G304 -- a tracked file of the repo
		if err != nil {
			return nil
		}
		content = string(data)
	}
	for _, change := range changes {
		if strings.Contains(content, change.old) {
			return splitFileLines(content)
		}
	}
	return nil
}

// writesKey says whether a changed file's old side wrote key where it would be a site.
func (c *doneChecker) writesKey(file *impact.ChangedFile, path, key string, where wirePaths) bool {
	if file.IsNew {
		return false
	}
	return len(changedKeyLines(configFormat(path), c.readOld(file, path), key, where)) > 0
}
