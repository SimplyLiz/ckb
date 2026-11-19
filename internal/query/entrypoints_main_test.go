package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

func TestProgramMains(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.go":          "// Command mini.\npackage main\n\nfunc main() {}\n",
		"lib/lib.go":       "package lib\n\nfunc main() {}\n",
		"main_test.go":     "package main\n",
		"tools/gen/gen.go": "package main\n\nfunc main() {}\n",
	}
	for path, src := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	doc := func(path string, symbols ...string) *scip.Document {
		d := &scip.Document{RelativePath: path}
		for _, s := range symbols {
			d.Symbols = append(d.Symbols, &scip.SymbolInformation{Symbol: s})
		}
		return d
	}
	idx := &scip.SCIPIndex{Documents: []*scip.Document{
		// A repo-root main: the old rule wanted /cmd/ in the path.
		doc("main.go", "scip-go gomod example.com/mini v1 `example.com/mini`/main()."),
		// A main in a library package doesn't start a program.
		doc("lib/lib.go", "scip-go gomod example.com/mini v1 `example.com/mini/lib`/main()."),
		doc("main_test.go", "scip-go gomod example.com/mini v1 `example.com/mini`/TestMain()."),
		doc("tools/gen/gen.go", "scip-go gomod example.com/mini v1 `example.com/mini/tools/gen`/main()."),
		// Other languages: the name is the convention.
		doc("src/main.rs", "rust-analyzer cargo mini 0.1.0 main()."),
		doc("src/App.java", "semanticdb maven . . com/example/App#main()."),
		// Not functions: a field and a local.
		doc("src/cfg.rs", "rust-analyzer cargo mini 0.1.0 Config#main.", "local 3"),
		doc("tests/cli_test.rs", "rust-analyzer cargo mini 0.1.0 main()."),
		// A test file's main is a test harness, not the program.
		doc("pkg/main_test.go", "scip-go gomod example.com/mini v1 `example.com/mini/pkg`/main()."),
	}}

	got := (&Engine{repoRoot: dir}).programMains(idx)
	var files2 []string
	for _, ep := range got {
		if ep.Type != "cli" || ep.Name != "main" {
			t.Errorf("%s: type %q name %q", ep.Location.FileId, ep.Type, ep.Name)
		}
		files2 = append(files2, ep.Location.FileId)
	}
	want := "main.go,src/App.java,src/main.rs,tools/gen/gen.go"
	if strings.Join(files2, ",") != want {
		t.Errorf("mains in %v, want %s", files2, want)
	}
}
