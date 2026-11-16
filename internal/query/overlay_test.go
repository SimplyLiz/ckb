package query

import (
	"context"
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
	indexmeta "github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/overlay"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

const overlayLib = `package lib

func Target(a int) int {
	return a
}

func Other() int {
	return Target(1)
}
`

// overlayEngine is an engine over a committed two-file repo whose index was
// built at that commit: Target (line 2) and Other (line 6) in lib/lib.go,
// Other calling Target on line 7, and app/app.go calling Target on line 3.
func overlayEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib/lib.go", overlayLib)
	write("app/app.go", "package app\n\nfunc Run() {\n\tlib.Target(2)\n}\n")
	write(".gitignore", "index.scip\n.ckb/\n")
	gitInit(t, root)
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}

	lib := fixtureDoc("lib/lib.go", []fixtureFunc{{symTarget, 2, 4}, {symOther, 6, 8}}, []fixtureRef{{symTarget, 7}})
	// The package clause's definition, as scip-go writes it in every file:
	// Layer 1 takes the package prefix from it.
	lib.Occurrences = append(lib.Occurrences, &scippb.Occurrence{
		Symbol: "scip-go gomod example.com/m v1 `example.com/m/lib`/", Range: []int32{0, 8, 11}, SymbolRoles: int32(scippb.SymbolRole_Definition),
	})
	lib.Symbols = append(lib.Symbols, &scippb.SymbolInformation{Symbol: "scip-go gomod example.com/m v1 `example.com/m/lib`/", DisplayName: "lib"})
	app := fixtureDoc("app/app.go", []fixtureFunc{{symRun, 2, 4}}, []fixtureRef{{symTarget, 3}})
	data, err := proto.Marshal(&scippb.Index{
		Metadata: &scippb.Metadata{ProjectRoot: "file://" + root, ToolInfo: &scippb.ToolInfo{
			Name: "scip-go", Arguments: []string{"--commit=" + strings.TrimSpace(string(head))},
		}},
		Documents: []*scippb.Document{lib, app},
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
	return engine, root
}

func defLine(t *testing.T, e *Engine, sym string) int32 {
	t.Helper()
	ref := e.scipAdapter.GetIndex().DefinitionIndex[sym]
	if ref == nil {
		return -1
	}
	return ref.Occ.Range[0]
}

func TestOverlay_FollowsTheWorkingTree(t *testing.T) {
	e, root := overlayEngine(t)
	if got := defLine(t, e, symOther); got != 6 {
		t.Fatalf("clean tree: Other at %d, want 6", got)
	}
	if info := e.OverlayInfo(); info == nil || len(info.Files) != 0 {
		t.Fatalf("clean tree: overlay info %+v, want no files", info)
	}

	// Two lines above Target, and Target's body replaced.
	edited := strings.Replace(overlayLib, "package lib\n", "package lib\n\n// Doc.\n", 1)
	edited = strings.Replace(edited, "\treturn a\n", "\treturn a + 1\n", 1)
	if err := os.WriteFile(filepath.Join(root, "lib/lib.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	e.overlay.checkedAt = e.overlay.checkedAt.Add(-overlayCheckInterval)
	if !e.RefreshOverlay(context.Background()) {
		t.Fatal("RefreshOverlay saw no change")
	}
	if got := defLine(t, e, symOther); got != 8 {
		t.Errorf("Other at %d, want 8 (two lines inserted above)", got)
	}
	if got := defLine(t, e, symTarget); got != 4 {
		t.Errorf("Target at %d, want 4", got)
	}
	// The call in Other moved with it; the unchanged file is untouched.
	var lines []int32
	for _, r := range e.scipAdapter.GetIndex().RefIndex[symTarget] {
		if r.Occ.SymbolRoles == 0 {
			lines = append(lines, r.Occ.Range[0])
		}
	}
	if len(lines) != 2 || lines[0] != 3 || lines[1] != 9 {
		t.Errorf("Target's references at %v, want [3 9] (app, then lib)", lines)
	}
	info := e.OverlayInfo()
	if info == nil || len(info.Files) != 1 || info.Files[0].State != overlay.StateRemapped {
		t.Fatalf("overlay info %+v, want lib/lib.go remapped", info)
	}

	// A diff-driven analysis reads the base, and the overlay comes back after.
	unpin := e.scipAdapter.PinBase()
	if got := defLine(t, e, symOther); got != 6 {
		t.Errorf("pinned: Other at %d, want the base's 6", got)
	}
	unpin()
	if got := defLine(t, e, symOther); got != 8 {
		t.Errorf("after unpin: Other at %d, want 8", got)
	}

	// Back to clean: the overlay goes away.
	if err := os.WriteFile(filepath.Join(root, "lib/lib.go"), []byte(overlayLib), 0o644); err != nil {
		t.Fatal(err)
	}
	e.overlay.checkedAt = e.overlay.checkedAt.Add(-overlayCheckInterval)
	e.RefreshOverlay(context.Background())
	if got := defLine(t, e, symOther); got != 6 {
		t.Errorf("reverted: Other at %d, want 6", got)
	}
}

func TestOverlay_DeletedFileLeavesTheIndex(t *testing.T) {
	e, root := overlayEngine(t)
	if err := os.Remove(filepath.Join(root, "app/app.go")); err != nil {
		t.Fatal(err)
	}
	e.overlay.checkedAt = e.overlay.checkedAt.Add(-overlayCheckInterval)
	e.RefreshOverlay(context.Background())
	if e.scipAdapter.GetIndex().GetDocument("app/app.go") != nil {
		t.Error("deleted file still in the index")
	}
	if e.scipAdapter.BaseIndex().GetDocument("app/app.go") == nil {
		t.Error("the base index lost the file")
	}
}

func TestOverlay_Disabled(t *testing.T) {
	t.Setenv("CKB_OVERLAY", "0")
	e, root := overlayEngine(t)
	if err := os.Remove(filepath.Join(root, "app/app.go")); err != nil {
		t.Fatal(err)
	}
	if e.RefreshOverlay(context.Background()) || e.scipAdapter.GetIndex().GetDocument("app/app.go") == nil {
		t.Error("CKB_OVERLAY=0 still patched the index")
	}
}

func TestOverlay_FreshnessSaysWhatItServes(t *testing.T) {
	e, root := overlayEngine(t)
	edited := strings.Replace(overlayLib, "package lib\n", "package lib\n\n// Doc.\n", 1)
	if err := os.WriteFile(filepath.Join(root, "lib/lib.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	freshnessCacheMu.Lock()
	freshnessCache = map[string]freshnessCacheEntry{}
	freshnessCacheMu.Unlock()
	e.overlay.checkedAt = e.overlay.checkedAt.Add(-overlayCheckInterval)
	e.RefreshOverlay(context.Background())

	f := e.IndexFreshnessFor(context.Background(), []string{"lib/lib.go"})
	if f == nil || f.Overlay == nil || f.Overlay.Covered != 1 || !f.AffectsResult {
		t.Fatalf("freshness %+v, want lib/lib.go affected and covered", f)
	}
	if !strings.Contains(f.Hint, "served from the overlay") {
		t.Errorf("hint %q", f.Hint)
	}

	unpin := e.scipAdapter.PinBase()
	defer unpin()
	if f := e.IndexFreshnessFor(context.Background(), []string{"lib/lib.go"}); f.Overlay != nil || strings.Contains(f.Hint, "overlay") {
		t.Errorf("pinned base still reports the overlay: %+v", f)
	}
}

func TestOverlay_NewCallerIsMarked(t *testing.T) {
	e, root := overlayEngine(t)
	edited := overlayLib + "\nfunc Extra() int {\n\treturn Target(3)\n}\n"
	if err := os.WriteFile(filepath.Join(root, "lib/lib.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	e.overlay.checkedAt = e.overlay.checkedAt.Add(-overlayCheckInterval)
	e.RefreshOverlay(context.Background())

	info := e.OverlayInfo()
	if info == nil || len(info.Files) != 1 {
		t.Fatalf("overlay info %+v", info)
	}
	if f := info.Files[0]; f.Declared != 1 || f.Referenced != 1 || f.Unresolved != 0 {
		t.Errorf("lib/lib.go: declared %d, referenced %d, unresolved %d; want 1, 1, 0", f.Declared, f.Referenced, f.Unresolved)
	}
	const symExtra = "scip-go gomod example.com/m v1 `example.com/m/lib`/Extra()."
	if got := defLine(t, e, symExtra); got != 10 {
		t.Errorf("Extra defined at %d, want 10", got)
	}

	resp, err := e.AnalyzeImpact(context.Background(), AnalyzeImpactOptions{SymbolId: symTarget, IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	var marked, unmarked []string
	for _, it := range resp.DirectImpact {
		if it.Source == "overlay" {
			marked = append(marked, it.StableId)
		} else {
			unmarked = append(unmarked, it.StableId)
		}
	}
	if len(marked) != 1 || marked[0] != symExtra {
		t.Errorf("overlay callers %v, want [Extra]", marked)
	}
	if len(unmarked) != 2 {
		t.Errorf("indexer callers %v, want Other and Run", unmarked)
	}
}

// An index built from a dirty working tree already holds those edits: the
// overlay must diff against the snapshot of that tree, not against the commit,
// or it moves everything a second time.
func TestOverlay_IndexBuiltFromADirtyTree(t *testing.T) {
	e, root := overlayEngine(t)
	edited := strings.Replace(overlayLib, "package lib\n", "package lib\n\n// Doc.\n", 1)
	if err := os.WriteFile(filepath.Join(root, "lib/lib.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pretend the loaded index was built from this tree.
	head := strings.TrimSpace(overlayGit(t, root, "rev-parse", "HEAD"))
	meta := &indexmeta.IndexMeta{Version: indexmeta.MetadataVersion, CommitHash: head}
	meta.RecordWorktree(root, true)
	if meta.WorktreeTree == "" {
		t.Fatal("no snapshot written")
	}
	if err := meta.Save(filepath.Join(root, ".ckb")); err != nil {
		t.Fatal(err)
	}
	e.overlay.base = nil // a new base: re-read the metadata
	e.overlay.checkedAt = e.overlay.checkedAt.Add(-overlayCheckInterval)
	e.RefreshOverlay(context.Background())
	if got := defLine(t, e, symOther); got != 6 {
		t.Errorf("Other at %d, want the index's own 6 (nothing changed since it was built)", got)
	}

	// An edit after the build is applied once.
	again := strings.Replace(edited, "// Doc.\n", "// Doc.\n// More.\n", 1)
	if err := os.WriteFile(filepath.Join(root, "lib/lib.go"), []byte(again), 0o644); err != nil {
		t.Fatal(err)
	}
	e.overlay.checkedAt = e.overlay.checkedAt.Add(-overlayCheckInterval)
	e.RefreshOverlay(context.Background())
	if got := defLine(t, e, symOther); got != 7 {
		t.Errorf("Other at %d, want 7 (one line added since the build)", got)
	}
}

func overlayGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
