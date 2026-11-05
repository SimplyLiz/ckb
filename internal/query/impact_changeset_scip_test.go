package query

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// wideConsumers is one document per consumer of Target, in cmd-less package paths.
func wideConsumers(n int) []*scippb.Document {
	docs := make([]*scippb.Document, 0, n)
	for i := 0; i < n; i++ {
		fn := fmt.Sprintf("scip-go gomod example.com/m v1 `example.com/m/c%d`/Use%d().", i, i)
		docs = append(docs, fixtureDoc(fmt.Sprintf("c%d/use.go", i),
			[]fixtureFunc{{fn, 2, 6}}, []fixtureRef{{symTarget, 3}}))
	}
	return docs
}

// The item list is a budgeted sample, and only the budget's edge may differ: every
// file is named, and the counts and the truncation say what was cut.
func TestAnalyzeChangeSet_ConsumerFilesSurviveTheItemBudget(t *testing.T) {
	engine := changeSetEngine(t, wideConsumers(40)...)
	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{DiffContent: bodyEditOfTarget})

	if len(resp.AffectedSymbols) != 20 || !resp.Truncated || resp.TruncationInfo == nil {
		t.Fatalf("items = %d, truncated = %v: want the default budget of 20, reported", len(resp.AffectedSymbols), resp.Truncated)
	}
	// app/app.go and 40 others are direct; cmd/main.go only reaches it through Run.
	if resp.TruncationInfo.OriginalCount != 42 || resp.Summary.DirectlyAffected != 41 {
		t.Errorf("truncation %+v, summary %+v: the cut hid consumers from the counts", resp.TruncationInfo, resp.Summary)
	}
	files := resp.ConsumerFiles
	if files == nil || len(files.Direct) != 41 || len(files.Transitive) != 1 || files.Transitive[0] != "cmd/main.go" {
		t.Fatalf("consumerFiles = %+v, want 41 direct and cmd/main.go transitive", files)
	}
	if files.Total != 44 || files.Truncated {
		t.Errorf("consumerFiles total %d truncated %v, want 44 (with 2 test files), complete", files.Total, files.Truncated)
	}
	if want := []string{"app/app_test.go", "lib/lib_test.go"}; strings.Join(files.Tests, ",") != strings.Join(want, ",") {
		t.Errorf("consumerFiles.tests = %v, want %v", files.Tests, want)
	}
}

func TestAnalyzeImpact_ConsumerFilesSurviveTheItemBudget(t *testing.T) {
	engine := changeSetEngine(t, wideConsumers(40)...)
	resp, err := engine.AnalyzeImpact(context.Background(), AnalyzeImpactOptions{SymbolId: symTarget})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Truncated || len(resp.DirectImpact) != 20 {
		t.Fatalf("direct = %d truncated = %v, want a budgeted list", len(resp.DirectImpact), resp.Truncated)
	}
	if resp.ConsumerFiles == nil || len(resp.ConsumerFiles.Direct) != 41 {
		t.Errorf("consumerFiles = %+v, want all 41 direct files", resp.ConsumerFiles)
	}
}

func TestBuildConsumerFiles_CutsTransitiveFirstAndSaysSo(t *testing.T) {
	items := func(n int, format string) []ImpactItem {
		out := make([]ImpactItem, n)
		for i := range out {
			out[i].Location = &LocationInfo{FileId: fmt.Sprintf(format, i)}
		}
		return out
	}
	files := BuildConsumerFiles(items(MaxConsumerFiles-5, "d/%04d.go"), items(50, "t/%04d.go"), items(3, "x/%04d_test.go"), "")
	if len(files.Direct) != MaxConsumerFiles-5 || len(files.Tests) != 3 || len(files.Transitive) != 2 {
		t.Errorf("direct %d tests %d transitive %d", len(files.Direct), len(files.Tests), len(files.Transitive))
	}
	if !files.Truncated || files.Total != MaxConsumerFiles+48 || !strings.Contains(files.Note, "showing") {
		t.Errorf("a cut must be visible: %+v", files)
	}

	partial := BuildConsumerFiles(items(1, "d/%d.go"), nil, nil, "stopped following callers after 100 symbols")
	if !partial.Truncated || !strings.Contains(partial.Note, "stopped following") {
		t.Errorf("an incomplete lookup must be visible: %+v", partial)
	}

	// A file reached both ways is direct, once.
	both := BuildConsumerFiles(items(1, "d/%d.go"), items(1, "d/%d.go"), nil, "")
	if both.Total != 1 || len(both.Transitive) != 0 {
		t.Errorf("listed twice: %+v", both)
	}
}

const signatureEditOfTarget = `diff --git a/lib/lib.go b/lib/lib.go
--- a/lib/lib.go
+++ b/lib/lib.go
@@ -1,6 +1,6 @@
 package lib
 
-func Target(a int) int {
+func Target(a int, b string) int {
 	return a
 }
 
`

const optionalAdditionToTarget = `diff --git a/lib/lib.go b/lib/lib.go
--- a/lib/lib.go
+++ b/lib/lib.go
@@ -1,6 +1,6 @@
 package lib
 
-func Target(a int) int {
+func Target(a int, opts ...string) int {
 	return a
 }
 
`

const removalOfTarget = `diff --git a/lib/lib.go b/lib/lib.go
--- a/lib/lib.go
+++ b/lib/lib.go
@@ -1,7 +1,3 @@
 package lib
 
-func Target(a int) int {
-	return a
-}
-
 func Other() {}
`

// The eval's result: a changed signature broke the build at low risk, because the
// score only counts. One consumer is enough.
func TestAnalyzeChangeSet_RiskFollowsTheDeclaration(t *testing.T) {
	engine := changeSetEngine(t)

	tests := []struct {
		name, diff, wantRisk, wantDecl, wantKind string
		wantFactor                               string
	}{
		{"signature changed", signatureEditOfTarget, "high", "changed", "signature-changed", "signature_changed_with_consumers"},
		{"removed", removalOfTarget, "high", "removed", "definition-removed", "deleted_with_consumers"},
		{"body only", bodyEditOfTarget, "low", "unchanged", "", ""},
		{"optional parameter", optionalAdditionToTarget, "low", "compatible", "compatible-signature-change", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{DiffContent: tt.diff})
			if resp.RiskScore.Level != tt.wantRisk {
				t.Errorf("risk = %s (%.2f): %s", resp.RiskScore.Level, resp.RiskScore.Score, resp.RiskScore.Explanation)
			}
			if resp.Summary.EstimatedRisk != tt.wantRisk {
				t.Errorf("summary risk = %s", resp.Summary.EstimatedRisk)
			}
			var target *ChangedSymbolInfo
			for i := range resp.ChangedSymbols {
				if resp.ChangedSymbols[i].SymbolID == symTarget {
					target = &resp.ChangedSymbols[i]
				}
			}
			if target == nil || target.Declaration != tt.wantDecl {
				t.Fatalf("changed symbol %+v, want declaration %q", target, tt.wantDecl)
			}

			var signal *ContractSignal
			for i := range resp.Contracts {
				if resp.Contracts[i].File == "lib/lib.go" {
					signal = &resp.Contracts[i]
				}
			}
			if tt.wantKind == "" {
				if signal != nil {
					t.Errorf("a body edit flagged %+v", signal)
				}
			} else if signal == nil || signal.Kind != tt.wantKind || !strings.HasPrefix(signal.Basis, "heuristic") {
				t.Errorf("contract = %+v, want %s, labelled a heuristic", signal, tt.wantKind)
			}

			hasFactor := tt.wantFactor == ""
			for _, f := range resp.RiskScore.Factors {
				hasFactor = hasFactor || f.Name == tt.wantFactor
			}
			if !hasFactor {
				t.Errorf("factors = %+v, want %s", resp.RiskScore.Factors, tt.wantFactor)
			}
		})
	}
}

// What agents read first: the files likely to break, and nothing else.
func TestAnalyzeChangeSet_ContractChangedNamesWhatBreaks(t *testing.T) {
	engine := changeSetEngine(t)

	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{DiffContent: signatureEditOfTarget})
	files := resp.ConsumerFiles
	if strings.Join(files.ContractChanged, ",") != "app/app.go" {
		t.Errorf("contractChanged = %v, want the direct consumer only, not cmd/main.go", files.ContractChanged)
	}
	if strings.Join(files.ContractChangedTests, ",") != "lib/lib_test.go" {
		t.Errorf("contractChangedTests = %v, want lib/lib_test.go", files.ContractChangedTests)
	}

	resp = runChangeSet(t, engine, AnalyzeChangeSetOptions{DiffContent: bodyEditOfTarget})
	if len(resp.ConsumerFiles.ContractChanged)+len(resp.ConsumerFiles.ContractChangedTests) != 0 {
		t.Errorf("a body edit names files as likely to break: %+v", resp.ConsumerFiles)
	}
}

// Every consumer updated in the same change: the build is fine, so no floor on an
// unexported symbol, and only the can't-see-other-repos floor on an exported one.
func TestAnalyzeChangeSet_ConsumersInTheDiffDontCount(t *testing.T) {
	engine := changeSetEngine(t)
	withCaller := signatureEditOfTarget + `diff --git a/app/app.go b/app/app.go
--- a/app/app.go
+++ b/app/app.go
@@ -5,3 +5,3 @@
 func Run() {
-	lib.Target(1)
+	lib.Target(1, "x")
 }
`
	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{DiffContent: withCaller})
	if resp.RiskScore.Level == "high" || resp.RiskScore.Level == "critical" {
		t.Errorf("risk = %s, with the only consumer in the diff: %s", resp.RiskScore.Level, resp.RiskScore.Explanation)
	}
	found := false
	for _, f := range resp.RiskScore.Factors {
		found = found || f.Name == "public_contract_changed"
	}
	if !found || resp.RiskScore.Level != "medium" {
		t.Errorf("exported Target: risk %s, factors %+v, want medium from public_contract_changed", resp.RiskScore.Level, resp.RiskScore.Factors)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// `ckb changes` promises the uncommitted working tree. A staged edit is uncommitted.
func TestGetGitDiff_WorkingTreeIncludesStaged(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{"staged.go": "package a\n", "unstaged.go": "package a\n", "other.go": "package a\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitInit(t, dir)
	for _, name := range []string{"staged.go", "unstaged.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, dir, "add", "staged.go")

	e := &Engine{repoRoot: dir}
	contains := func(diff, name string) bool { return strings.Contains(diff, "a/"+name) }

	working, err := e.getGitDiff(false, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(working, "staged.go") || !contains(working, "unstaged.go") {
		t.Errorf("working tree diff misses a change:\n%s", working)
	}

	staged, err := e.getGitDiff(true, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(staged, "staged.go") || contains(staged, "unstaged.go") {
		t.Errorf("staged diff is wrong:\n%s", staged)
	}
}

func TestGetGitDiff_NoCommitYet(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	if _, err := (&Engine{repoRoot: dir}).getGitDiff(false, "HEAD"); err != nil {
		t.Errorf("a repo with no commits has an empty diff, not an error: %v", err)
	}
}

// A comment changed: nothing downstream can have noticed.
func TestAnalyzeChangeSet_CommentOnlyHasNoBlastRadius(t *testing.T) {
	engine := changeSetEngine(t)
	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{DiffContent: `diff --git a/lib/lib.go b/lib/lib.go
--- a/lib/lib.go
+++ b/lib/lib.go
@@ -3,3 +3,3 @@
 func Target(a int) int {
-	// old words
+	// new words
 	return a
`})
	if len(resp.ChangedSymbols) != 1 || resp.ChangedSymbols[0].Note == "" {
		t.Fatalf("the change should still be listed, with its note: %+v", resp.ChangedSymbols)
	}
	if len(resp.AffectedSymbols) != 0 || resp.Summary.DirectlyAffected != 0 || resp.ConsumerFiles.Total != 0 {
		t.Errorf("comment-only change has a blast radius: %+v, %+v", resp.Summary, resp.ConsumerFiles)
	}
	if resp.RiskScore.Level != "low" || !strings.Contains(resp.RiskScore.Explanation, "Only comments") {
		t.Errorf("risk = %+v", resp.RiskScore)
	}
}

func TestAnalyzeChangeSet_NoChangesIsNotARiskLevel(t *testing.T) {
	engine := changeSetEngine(t)
	gitInit(t, engine.repoRoot)
	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})
	if resp.RiskScore.Level != RiskLevelNotAssessed || resp.Summary.EstimatedRisk != RiskLevelNotAssessed {
		t.Errorf("risk = %+v, summary = %+v", resp.RiskScore, resp.Summary)
	}
	if resp.RiskScore.Explanation != NothingToAssess {
		t.Errorf("explanation = %q", resp.RiskScore.Explanation)
	}
}

// Search falls back to tree-sitter without an index, and `ckb impact` with that ID
// said "not found, use ckb search", which hands out the same ID again.
func TestAnalyzeImpact_TreesitterIDSaysAnIndexIsNeeded(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()
	id := generateTreesitterSymbolId("a.go", "F", "function", 1)
	_, err := engine.AnalyzeImpact(context.Background(), AnalyzeImpactOptions{SymbolId: id})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "needs a SCIP index") || strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v", err)
	}
}
