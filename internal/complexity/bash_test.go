//go:build cgo

package complexity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// replayScript is shaped like the scripts of a repo that drives Python tools
// from Bash: a lib sourced through $SCRIPT_DIR, helpers, an argument loop and
// top-level commands after the functions.
const replayScript = `#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

log() {
  echo "[$(date +%T)] $*" >&2
}

wait_for_port() {
  local port=$1 tries=0
  until nc -z localhost "$port"; do
    tries=$((tries + 1))
    if [[ $tries -gt 30 ]]; then
      return 1
    fi
    sleep 1
  done
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --mode) MODE=$2; shift 2 ;;
      --dry-run) DRY_RUN=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) echo "unknown: $1" >&2; exit 2 ;;
    esac
  done
}

function run_replay {
  for f in "$@"; do
    if [[ -f $f && -r $f ]] || [[ -n ${FORCE:-} ]]; then
      python3 scripts/linux/replay.py --input "$f"
    elif [[ -d $f ]]; then
      log "skipping dir $f"
    else
      log "missing $f" || true
    fi
  done
}

parse_args "$@"
if [[ ${DRY_RUN:-0} -eq 1 ]]; then
  exit 0
fi
run_replay "$@"
./cleanup.sh
`

func TestAnalyzeSource_Bash(t *testing.T) {
	a := NewAnalyzer()
	fc, err := a.AnalyzeSource(context.Background(), "replay.sh", []byte(replayScript), LangBash)
	if err != nil {
		t.Fatal(err)
	}
	if fc.Error != "" {
		t.Fatalf("analysis error: %s", fc.Error)
	}

	type want struct {
		start, end, cyclomatic, cognitive int
	}
	// Cyclomatic: 1 + if/elif/for/while/until + one per case arm + one per
	// && or || (command lists and inside [[ ]]); -gt, + and | don't count.
	// Cognitive: each of those adds 1 + its nesting depth.
	wants := map[string]want{
		"log":           {7, 9, 1, 0},
		"wait_for_port": {11, 20, 3, 3},  // until; if inside it (1+1)
		"parse_args":    {22, 31, 6, 13}, // while; 4 arms at depth 2 (case nests)
		// for 1; if 2; its || and && 3+3; elif 3; "|| true" in else 3
		"run_replay": {33, 43, 7, 15},
		// "cd ... && pwd" in the substitution; the if
		TopLevelName: {2, 50, 3, 2},
	}
	got := map[string]ComplexityResult{}
	for _, f := range fc.Functions {
		got[f.Name] = f
	}
	// Top-level lines: set, SCRIPT_DIR, source, then 45-50 (the if is 3).
	if top := got[TopLevelName]; top.Lines != 9 {
		t.Errorf("toplevel Lines = %d, want 9", top.Lines)
	}
	if len(got) != len(wants) {
		t.Errorf("got %d units %v, want %d", len(got), keys(got), len(wants))
	}
	for name, w := range wants {
		f, ok := got[name]
		if !ok {
			t.Errorf("missing unit %q", name)
			continue
		}
		if f.StartLine != w.start || f.EndLine != w.end {
			t.Errorf("%s: lines %d-%d, want %d-%d", name, f.StartLine, f.EndLine, w.start, w.end)
		}
		if f.Cyclomatic != w.cyclomatic {
			t.Errorf("%s: cyclomatic %d, want %d", name, f.Cyclomatic, w.cyclomatic)
		}
		if f.Cognitive != w.cognitive {
			t.Errorf("%s: cognitive %d, want %d", name, f.Cognitive, w.cognitive)
		}
	}
}

// A script with only functions has no top-level unit; one with only comments
// and a shebang has nothing.
func TestAnalyzeSource_BashTopLevelOnlyWhenPresent(t *testing.T) {
	a := NewAnalyzer()
	fc, _ := a.AnalyzeSource(context.Background(), "lib.sh", []byte("#!/bin/sh\n# helpers\nf() { :; }\n"), LangBash)
	if fc.FunctionCount != 1 || fc.Functions[0].Name != "f" {
		t.Errorf("functions-only script: got %+v", fc.Functions)
	}
	fc, _ = a.AnalyzeSource(context.Background(), "empty.sh", []byte("#!/bin/sh\n# nothing\n"), LangBash)
	if fc.FunctionCount != 0 {
		t.Errorf("comment-only script: got %+v", fc.Functions)
	}
}

func TestAnalyzeFile_BashByExtensionAndShebang(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.sh":      "x() { if true; then :; fi; }\n",
		"b.bash":    "x() { if true; then :; fi; }\n",
		"deploy":    "#!/usr/bin/env bash\nx() { if true; then :; fi; }\n",
		"run-env-s": "#!/usr/bin/env -S bash -e\nx() { if true; then :; fi; }\n",
		"plainsh":   "#!/bin/sh\nx() { if true; then :; fi; }\n",
		"pyscript":  "#!/usr/bin/env python3\nprint(1)\n",
		"README":    "x() { if true; then :; fi; }\n",
		"notes.txt": "#!/bin/bash\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := NewAnalyzer()
	for name, wantBash := range map[string]bool{
		"a.sh": true, "b.bash": true, "deploy": true, "run-env-s": true, "plainsh": true,
		"pyscript": false, "README": false, "notes.txt": false,
	} {
		fc, err := a.AnalyzeFile(context.Background(), filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		isBash := fc.Error == "" && fc.Language == LangBash
		if isBash != wantBash {
			t.Errorf("%s: bash=%v (lang %q, error %q), want %v", name, isBash, fc.Language, fc.Error, wantBash)
			continue
		}
		if wantBash && (fc.FunctionCount != 1 || fc.MaxCyclomatic != 2) {
			t.Errorf("%s: got %+v", name, fc.Functions)
		}
	}
}

func TestLanguageFromShebang(t *testing.T) {
	cases := map[string]bool{
		"#!/bin/bash\n":               true,
		"#!/bin/bash -eu\n":           true,
		"#! /bin/sh\n":                true,
		"#!/usr/bin/env bash\n":       true,
		"#!/usr/bin/env -S bash -e\n": true,
		"#!/usr/bin/env FOO=1 sh\n":   true,
		"#!/usr/bin/dash":             true,
		"#!/usr/bin/env python3\n":    false,
		"#!/usr/bin/env\n":            false,
		"# not a shebang\n":           false,
		"":                            false,
	}
	for head, want := range cases {
		lang, ok := LanguageFromShebang([]byte(head))
		if ok != want || (ok && lang != LangBash) {
			t.Errorf("%q: got %q %v, want bash=%v", head, lang, ok, want)
		}
	}
}

func keys(m map[string]ComplexityResult) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
