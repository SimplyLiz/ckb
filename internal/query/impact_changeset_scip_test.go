package query

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

const (
	symTarget = "scip-go gomod example.com/m v1 `example.com/m/lib`/Target()."
	symOther  = "scip-go gomod example.com/m v1 `example.com/m/lib`/Other()."
	symRun    = "scip-go gomod example.com/m v1 `example.com/m/app`/Run()."
	symMain   = "scip-go gomod example.com/m v1 `example.com/m/cmd`/Main()."
	symTest   = "scip-go gomod example.com/m v1 `example.com/m/lib`/TestTarget()."
	symAppT   = "scip-go gomod example.com/m v1 `example.com/m/app`/TestRun()."
)

// fixtureFunc is a function in a fixture document: declared on line, closing on end.
type fixtureFunc struct {
	symbol    string
	line, end int32
}

// fixtureRef is a use of a symbol at a line.
type fixtureRef struct {
	symbol string
	line   int32
}

// fixtureDoc builds a document the way scip-go writes one.
func fixtureDoc(path string, funcs []fixtureFunc, refs []fixtureRef) *scippb.Document {
	doc := &scippb.Document{RelativePath: path, Language: "go"}
	for _, fn := range funcs {
		doc.Symbols = append(doc.Symbols, &scippb.SymbolInformation{
			Symbol: fn.symbol,
			Kind:   scippb.SymbolInformation_Function,
		})
		doc.Occurrences = append(doc.Occurrences, &scippb.Occurrence{
			Symbol:         fn.symbol,
			Range:          []int32{fn.line, 5, 11},
			SymbolRoles:    int32(scippb.SymbolRole_Definition),
			EnclosingRange: []int32{fn.line, 0, fn.end, 1},
		})
	}
	for _, ref := range refs {
		doc.Occurrences = append(doc.Occurrences, &scippb.Occurrence{
			Symbol: ref.symbol,
			Range:  []int32{ref.line, 1, 7},
		})
	}
	return doc
}

// changeSetEngine is an engine over a small repo with a SCIP index, for testing
// AnalyzeChangeSet end to end: Target and Other in lib, called by Run and by tests.
//
//	lib/lib.go       Target (line 3), Other (line 8)
//	lib/lib_test.go  TestTarget -> Target, plus a package-level use of Target
//	app/app.go       Run -> Target
//	app/app_test.go  TestRun -> Run
//	cmd/main.go      Main -> Run
func changeSetEngine(t *testing.T, extra ...*scippb.Document) *Engine {
	t.Helper()
	root := t.TempDir()

	docs := []*scippb.Document{
		fixtureDoc("lib/lib.go",
			[]fixtureFunc{{symTarget, 2, 5}, {symOther, 7, 10}}, nil),
		fixtureDoc("lib/lib_test.go",
			[]fixtureFunc{{symTest, 5, 8}}, []fixtureRef{{symTarget, 6}}),
		fixtureDoc("app/app.go",
			[]fixtureFunc{{symRun, 4, 8}}, []fixtureRef{{symTarget, 5}}),
		fixtureDoc("app/app_test.go",
			[]fixtureFunc{{symAppT, 4, 8}}, []fixtureRef{{symRun, 5}}),
		fixtureDoc("cmd/main.go",
			[]fixtureFunc{{symMain, 4, 8}}, []fixtureRef{{symRun, 5}}),
	}
	docs = append(docs, extra...)
	data, err := proto.Marshal(&scippb.Index{
		Metadata:  &scippb.Metadata{ProjectRoot: "file://" + root, ToolInfo: &scippb.ToolInfo{Name: "scip-go"}},
		Documents: docs,
	})
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "index.scip")
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, ".ckb"), 0o755); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := storage.Open(root, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.DefaultConfig()
	cfg.RepoRoot = root
	cfg.Backends.Scip.Enabled = true
	cfg.Backends.Scip.IndexPath = indexPath
	engine, err := NewEngine(root, db, logger, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if engine.scipAdapter == nil || !engine.scipAdapter.IsAvailable() {
		t.Fatal("fixture index did not load")
	}
	return engine
}

// bodyEditOfTarget changes only Target's body.
const bodyEditOfTarget = `diff --git a/lib/lib.go b/lib/lib.go
--- a/lib/lib.go
+++ b/lib/lib.go
@@ -1,6 +1,6 @@
 package lib
 
 func Target(a int) int {
-	return a
+	return a + 1
 }
 
`

func runChangeSet(t *testing.T, engine *Engine, opts AnalyzeChangeSetOptions) *AnalyzeChangeSetResponse {
	t.Helper()
	resp, err := engine.AnalyzeChangeSet(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-q", "--allow-empty", "-m", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func affectedPaths(items []ImpactItem) map[string]int {
	paths := make(map[string]int)
	for _, item := range items {
		if item.Location != nil {
			paths[item.Location.FileId] = item.Distance
		}
	}
	return paths
}

func testFor(tests []AffectedTest, path string) *AffectedTest {
	for i := range tests {
		if tests[i].FilePath == path {
			return &tests[i]
		}
	}
	return nil
}

// A test that calls the changed symbol is what a change-safety answer has to name,
// and it must not count as a production consumer: not in the list, not in the score.
func TestAnalyzeChangeSet_TestsReportedAsTests(t *testing.T) {
	engine := changeSetEngine(t)
	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{DiffContent: bodyEditOfTarget})

	direct := testFor(resp.AffectedTests, "lib/lib_test.go")
	if direct == nil {
		t.Fatalf("lib/lib_test.go calls Target and is missing: %+v", resp.AffectedTests)
	}
	if direct.Reason != "direct" {
		t.Errorf("lib/lib_test.go reason = %q, want direct", direct.Reason)
	}
	if len(direct.TestNames) != 1 || direct.TestNames[0] != "TestTarget" {
		t.Errorf("TestNames = %v, want TestTarget", direct.TestNames)
	}
	if reached := testFor(resp.AffectedTests, "app/app_test.go"); reached == nil || reached.Reason != "transitive" {
		t.Errorf("app/app_test.go reaches Target through Run: %+v", reached)
	}

	paths := affectedPaths(resp.AffectedSymbols)
	for _, p := range []string{"lib/lib_test.go", "app/app_test.go"} {
		if _, ok := paths[p]; ok {
			t.Errorf("%s is listed as a production consumer: %v", p, paths)
		}
	}
	if paths["app/app.go"] != 1 || paths["cmd/main.go"] != 2 {
		t.Errorf("production consumers = %v, want app/app.go at 1 and cmd/main.go at 2", paths)
	}
	if resp.Summary.DirectlyAffected != 1 || resp.Summary.TransitivelyAffected != 1 {
		t.Errorf("summary counts tests: %+v", resp.Summary)
	}
}

// IncludeTests keeps its meaning: test code counts as a consumer.
func TestAnalyzeChangeSet_IncludeTestsCountsThem(t *testing.T) {
	engine := changeSetEngine(t)
	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{DiffContent: bodyEditOfTarget, IncludeTests: true})
	if _, ok := affectedPaths(resp.AffectedSymbols)["lib/lib_test.go"]; !ok {
		t.Errorf("IncludeTests: lib/lib_test.go not in %v", affectedPaths(resp.AffectedSymbols))
	}
	if testFor(resp.AffectedTests, "lib/lib_test.go") == nil {
		t.Errorf("IncludeTests: lib/lib_test.go not in affectedTests")
	}
}
