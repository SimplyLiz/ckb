//go:build cgo

package symbols

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/complexity"
)

const loadmodeReplay = `#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$SCRIPT_DIR/../.."
source "$SCRIPT_DIR/lib.sh"
. ./common.sh

main() {
  python3 scripts/linux/replay.py --mode "$1"
  python3 -u "$ROOT/tools/stats.py" --json
  bash ./other.sh "$1"
  ./missing.sh
  python3 -m tools.report
  python3 -c "print(1)"
  echo ./lib.sh
  deploy.sh
  /usr/bin/env python3 tools/stats.py
  source ../../../etc/profile
  if OUT=$(./other.sh); then
    timeout 30 "$SCRIPT_DIR/other.sh" | tee log.txt
  fi
}

main "$@"
`

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestExtractScriptCalls(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"scripts/linux/loadmode-replay.sh": loadmodeReplay,
		"scripts/linux/lib.sh":             "#!/bin/bash\n",
		"scripts/linux/common.sh":          "#!/bin/bash\n",
		"scripts/linux/other.sh":           "#!/bin/bash\n",
		"scripts/linux/replay.py":          "print(1)\n",
		"tools/stats.py":                   "print(1)\n",
		"tools/report.py":                  "print(1)\n",
	})
	e := NewExtractor()
	calls, err := e.ExtractScriptCalls(context.Background(), root, filepath.Join(root, "scripts/linux/loadmode-replay.sh"))
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, c := range calls {
		if c.From != "scripts/linux/loadmode-replay.sh" {
			t.Errorf("From = %q", c.From)
		}
		v := ""
		if c.ViaVariable {
			v = " var"
		}
		got = append(got, strings.TrimSpace(
			strconv.Itoa(c.Line)+" "+c.Kind+" "+c.Interpreter+" "+c.To+v))
	}
	want := []string{
		"5 source  scripts/linux/lib.sh var",     // "$SCRIPT_DIR/lib.sh"
		"6 source  scripts/linux/common.sh",      // ./common.sh, script dir
		"9 exec python3 scripts/linux/replay.py", // repo root
		"10 exec python3 tools/stats.py var",     // "$ROOT/...": parent walk
		"11 exec bash scripts/linux/other.sh",
		"13 exec python3 tools/report.py",     // -m tools.report
		"17 exec python3 tools/stats.py",      // through /usr/bin/env
		"19 exec  scripts/linux/other.sh",     // inside $(...)
		"20 exec  scripts/linux/other.sh var", // after timeout's duration
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, c := range calls {
		if (c.ViaVariable && c.Confidence != 0.7) || (!c.ViaVariable && c.Confidence != 0.9) {
			t.Errorf("%s line %d: confidence %v", c.To, c.Line, c.Confidence)
		}
	}
}

func TestExtractScriptCalls_NotAScript(t *testing.T) {
	root := writeRepo(t, map[string]string{"a.py": "import os\n", "b.sh": "#!/bin/sh\n"})
	calls, err := NewExtractor().ExtractScriptCalls(context.Background(), root, filepath.Join(root, "a.py"))
	if err != nil || calls != nil {
		t.Errorf("python file: %v %v", calls, err)
	}
}

func TestExtractScriptCallsDirectory(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"run":            "#!/bin/sh\n. lib/env.sh\n./tools/build.sh\n", // extensionless, shebang
		"lib/env.sh":     "export X=1\n",
		"tools/build.sh": "source ../lib/env.sh\n",
		"README":         "./tools/build.sh\n", // no shebang: not a script
	})
	calls, err := NewExtractor().ExtractScriptCallsDirectory(context.Background(), root, DirOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range calls {
		got = append(got, c.From+" -> "+c.To)
	}
	sort.Strings(got)
	want := []string{"run -> lib/env.sh", "run -> tools/build.sh", "tools/build.sh -> lib/env.sh"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractSource_BashFunctions(t *testing.T) {
	e := NewExtractor()
	syms, err := e.ExtractSource(context.Background(), "x.sh", []byte(loadmodeReplay+"\nfunction cleanup {\n  rm -f log.txt\n}\n"), complexity.LangBash)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range syms {
		got = append(got, s.Name+":"+s.Kind+":"+strconv.Itoa(s.Line)+"-"+strconv.Itoa(s.EndLine)+":"+s.Signature)
	}
	want := []string{"main:function:8-22:main()", "cleanup:function:26-28:function cleanup"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestShellPathWord(t *testing.T) {
	cases := []struct {
		raw, path  string
		viaVar, ok bool
	}{
		{"./x.sh", "./x.sh", false, true},
		{`"$DIR/x.sh"`, "/x.sh", true, true},
		{`"${ROOT}/a/b.py"`, "/a/b.py", true, true},
		{`"$(dirname "$0")/lib.sh"`, "/lib.sh", true, true},
		{`"$(cd "$(dirname "$0")" && pwd)/../x.sh"`, "/../x.sh", true, true},
		{"`dirname $0`/x.sh", "/x.sh", true, true},
		{`"$DIR/$NAME.sh"`, "", false, false},
		{`"$DIR"`, "", false, false},
		{"lib/*.sh", "", false, false},
		{"~/x.sh", "", false, false},
		{`''`, "", false, false},
	}
	for _, c := range cases {
		p, v, ok := shellPathWord(c.raw)
		if p != c.path || v != c.viaVar || ok != c.ok {
			t.Errorf("%s: got %q %v %v, want %q %v %v", c.raw, p, v, ok, c.path, c.viaVar, c.ok)
		}
	}
}
