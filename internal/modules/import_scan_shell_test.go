//go:build cgo

package modules

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/config"
)

// A shell script's `python3 path/x.py`, `./y.sh` and `source lib.sh` are
// imports: without them a repo of scripts had no edges between its parts.
func TestScanFile_ShellScriptCallsAreRelativeImports(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("scripts/linux/replay.py", "print('x')\n")
	write("scripts/lib/common.sh", "log() { echo \"$@\"; }\n")
	write("scripts/linux/helper.sh", "#!/bin/bash\necho hi\n")
	write("scripts/linux/loadmode-replay.sh", `#!/usr/bin/env bash
source ../lib/common.sh
./helper.sh
python3 scripts/linux/replay.py --port 8081 "$@"
curl http://127.0.0.1:8081/health
`)
	// Extensionless, found by its shebang.
	write("tools/deploy", "#!/bin/sh\n. scripts/lib/common.sh\n")

	cfg := config.DefaultConfig()
	s := NewImportScanner(&cfg.ImportScan, slog.New(slog.NewTextHandler(io.Discard, nil)))

	edges, err := s.ScanFile(filepath.Join(root, "scripts/linux/loadmode-replay.sh"), root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range edges {
		got[e.To] = true
		if e.From != "scripts/linux/loadmode-replay.sh" {
			t.Errorf("From = %q", e.From)
		}
	}
	for _, want := range []string{"../lib/common.sh", "./helper.sh", "./replay.py"} {
		if !got[want] {
			t.Errorf("missing edge to %s; got %v", want, got)
		}
	}
	if len(edges) != 3 {
		t.Errorf("want 3 edges, got %d: %v", len(edges), got)
	}

	classifier := NewImportClassifier(&ModuleContext{RepoRoot: root})
	for _, e := range edges {
		classifier.ClassifyEdge(e)
		if e.Kind != LocalFile {
			t.Errorf("%s classified %s, want local file", e.To, e.Kind)
		}
	}

	edges, err = s.ScanFile(filepath.Join(root, "tools/deploy"), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].To != "../scripts/lib/common.sh" {
		t.Fatalf("extensionless script edges = %+v", edges)
	}
}
