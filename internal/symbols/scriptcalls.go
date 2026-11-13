package symbols

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ScriptCall is a file-level edge from a shell script to another file of the
// repository: a script it sources, or a script or program file it runs.
type ScriptCall struct {
	// From is the calling script, repo-relative with forward slashes.
	From string `json:"from"`
	// To is the called file, repo-relative with forward slashes. It existed
	// when the edge was extracted.
	To string `json:"to"`
	// Line is the 1-based line of the command.
	Line int `json:"line"`
	// Kind is ScriptCallSource ("source lib.sh", ". lib.sh") or
	// ScriptCallExec ("./x.sh", "bash x.sh", "python3 y.py").
	Kind string `json:"kind"`
	// Interpreter is the program named before the path ("bash", "python3"),
	// empty when the file is run directly or sourced.
	Interpreter string `json:"interpreter,omitempty"`
	// Raw is the path argument as written.
	Raw string `json:"raw"`
	// ViaVariable is true when the path starts with a variable or command
	// substitution ("$SCRIPT_DIR/x.sh", "$(dirname "$0")/x.sh") that was
	// dropped; To is then the first existing match, not a certainty.
	ViaVariable bool `json:"viaVariable,omitempty"`
	// Confidence is 0.9 for a literal path, 0.7 for one through a variable.
	Confidence float64 `json:"confidence"`
}

// Script call kinds.
const (
	ScriptCallSource = "source"
	ScriptCallExec   = "exec"
)

// scriptInterpreters run the file named by their first non-flag argument.
var scriptInterpreters = map[string]bool{
	"bash": true, "sh": true, "dash": true, "ksh": true, "zsh": true,
	"python": true, "python2": true, "python3": true,
	"node": true, "ruby": true, "perl": true, "php": true,
	"Rscript": true, "julia": true, "lua": true, "tclsh": true,
}

// interpreterSubcommands are interpreters whose script follows a subcommand.
var interpreterSubcommands = map[string]string{
	"deno": "run",
	"bun":  "run",
	"uv":   "run",
}

// commandWrappers run their arguments as a command line.
var commandWrappers = map[string]bool{
	"exec": true, "nohup": true, "time": true, "sudo": true, "env": true,
	"nice": true, "command": true, "timeout": true,
}

// flagsWithValue are interpreter flags that take the next argument, so it is
// not the script. "-c" (inline code) ends the search: there is no script file.
var flagsWithValue = map[string]bool{
	"-W": true, "-X": true, "-o": true, "-O": true, "--rcfile": true, "--init-file": true,
}

// leadingExpansion matches a path that starts with one variable or command
// substitution followed by a literal rest: "$DIR/x.sh", "${ROOT}/a/b.py",
// "$(dirname $0)/lib.sh", "$(cd $(dirname $0) && pwd)/../x.sh".
var leadingExpansion = regexp.MustCompile(`^(?:\$[A-Za-z_][A-Za-z0-9_]*|\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$\(.*\)|` + "`[^`]*`" + `)(/[^$` + "`" + `]*)$`)

// shellPathWord turns a shell word as written into a path. It drops quotes and
// reports whether a leading expansion was removed. ok is false when the word
// holds an expansion anywhere else, a glob, or is empty.
func shellPathWord(raw string) (path string, viaVar bool, ok bool) {
	w := strings.TrimSpace(raw)
	w = strings.ReplaceAll(w, `"`, "")
	w = strings.ReplaceAll(w, `'`, "")
	if w == "" {
		return "", false, false
	}
	if strings.ContainsAny(w, "$`") {
		m := leadingExpansion.FindStringSubmatch(w)
		if m == nil {
			return "", false, false
		}
		w = m[1]
		viaVar = true
	}
	if strings.ContainsAny(w, "*?[{}~") {
		return "", false, false
	}
	return w, viaVar, true
}

// resolveScriptPath finds the repository file a script path refers to.
// script is the calling script's absolute path. A literal relative path is
// tried against the script's directory, then the repository root (the two
// working directories scripts are usually run from). A path through a
// variable is tried against the script's directory and each parent up to the
// root, since such variables usually hold the script's directory or a parent
// of it. Absolute paths are accepted when inside the root. The result is
// repo-relative with forward slashes; ok is false when no candidate exists as
// a regular file inside the root.
func resolveScriptPath(repoRoot, script, p string, viaVar bool) (string, bool) {
	root := filepath.Clean(repoRoot)
	var candidates []string
	switch {
	case viaVar:
		rest := strings.TrimLeft(p, "/")
		for dir := filepath.Dir(script); ; dir = filepath.Dir(dir) {
			candidates = append(candidates, filepath.Join(dir, rest))
			if !isWithin(root, dir) || dir == root || dir == filepath.Dir(dir) {
				break
			}
		}
		if !slices.Contains(candidates, filepath.Join(root, rest)) {
			candidates = append(candidates, filepath.Join(root, rest))
		}
	case filepath.IsAbs(p):
		candidates = []string{filepath.Clean(p)}
	default:
		candidates = []string{filepath.Join(filepath.Dir(script), p), filepath.Join(root, p)}
	}
	for _, c := range candidates {
		if !isWithin(root, c) || c == script {
			continue
		}
		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() {
			rel, err := filepath.Rel(root, c)
			if err != nil {
				continue
			}
			return filepath.ToSlash(rel), true
		}
	}
	return "", false
}

// isWithin reports whether p is root or below it. Both must be clean.
func isWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// pythonModulePath turns "pkg.mod" (from "python -m pkg.mod") into the files
// that may hold it.
func pythonModulePath(mod string) []string {
	base := strings.ReplaceAll(mod, ".", "/")
	return []string{base + ".py", base + "/__main__.py"}
}

// scriptCommand is one simple command as the extractor sees it: its name and
// arguments as written, with the line it starts on.
type scriptCommand struct {
	name string
	args []string
	line int
}

// target is a path a command refers to, before resolution.
type target struct {
	kind        string
	interpreter string
	raw         string
	module      bool // raw is a python module name
}

// commandTarget returns the file a command sources or runs, if any.
func commandTarget(cmd scriptCommand) (target, bool) {
	name, args := cmd.name, cmd.args
	for depth := 0; depth < 4; depth++ {
		// "/usr/bin/env" and "/bin/bash" are the programs env and bash; a
		// relative path with a slash ("./bash") is a repository file.
		base := stripQuotes(name)
		if filepath.IsAbs(base) {
			base = filepath.Base(base)
		}
		switch {
		case base == "source" || base == ".":
			if len(args) == 0 {
				return target{}, false
			}
			return target{kind: ScriptCallSource, raw: args[0]}, true

		case commandWrappers[base]:
			// Skip the wrapper's own flags and env's VAR=value assignments,
			// and timeout's duration, then treat the rest as the command.
			i := 0
			for i < len(args) && (strings.HasPrefix(args[i], "-") || strings.Contains(args[i], "=") ||
				(base == "timeout" && i == 0 && isDuration(args[i]))) {
				i++
			}
			if i >= len(args) {
				return target{}, false
			}
			name, args = args[i], args[i+1:]
			continue

		case scriptInterpreters[base] || interpreterSubcommands[base] != "":
			rest := args
			if sub := interpreterSubcommands[base]; sub != "" {
				if len(rest) == 0 || rest[0] != sub {
					return target{}, false
				}
				rest = rest[1:]
			}
			for i := 0; i < len(rest); i++ {
				a := rest[i]
				switch {
				case a == "-c" || a == "-e" || a == "--eval" || a == "-":
					return target{}, false // inline code or stdin
				case a == "-m" && strings.HasPrefix(base, "python"):
					if i+1 < len(rest) {
						return target{kind: ScriptCallExec, interpreter: base, raw: rest[i+1], module: true}, true
					}
					return target{}, false
				case flagsWithValue[a]:
					i++
				case strings.HasPrefix(a, "-"):
				default:
					return target{kind: ScriptCallExec, interpreter: base, raw: a}, true
				}
			}
			return target{}, false

		case strings.Contains(stripQuotes(name), "/"):
			// "./x.sh", "scripts/x.sh", "$DIR/x.sh": run directly. A bare
			// "x.sh" is looked up on PATH, not in the working directory.
			return target{kind: ScriptCallExec, raw: name}, true
		}
		return target{}, false
	}
	return target{}, false
}

func stripQuotes(s string) string {
	return strings.NewReplacer(`"`, "", `'`, "").Replace(s)
}

func isDuration(s string) bool {
	s = strings.TrimRight(s, "smhd")
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

// resolveScriptCalls turns a script's commands into edges to existing files.
// script is the script's absolute path, rel its repo-relative slash path.
func resolveScriptCalls(repoRoot, script, rel string, cmds []scriptCommand) []ScriptCall {
	var calls []ScriptCall
	seen := make(map[string]bool)
	for _, cmd := range cmds {
		t, ok := commandTarget(cmd)
		if !ok {
			continue
		}
		var to string
		var viaVar, found bool
		if t.module {
			for _, cand := range pythonModulePath(stripQuotes(t.raw)) {
				if to, found = resolveScriptPath(repoRoot, script, cand, false); found {
					break
				}
			}
		} else {
			var p string
			if p, viaVar, ok = shellPathWord(t.raw); !ok {
				continue
			}
			to, found = resolveScriptPath(repoRoot, script, p, viaVar)
		}
		if !found {
			continue
		}
		key := fmt.Sprintf("%s\x00%s\x00%d", to, t.kind, cmd.line)
		if seen[key] {
			continue
		}
		seen[key] = true
		conf := 0.9
		if viaVar {
			conf = 0.7
		}
		calls = append(calls, ScriptCall{
			From:        rel,
			To:          to,
			Line:        cmd.line,
			Kind:        t.kind,
			Interpreter: t.interpreter,
			Raw:         t.raw,
			ViaVariable: viaVar,
			Confidence:  conf,
		})
	}
	return calls
}
