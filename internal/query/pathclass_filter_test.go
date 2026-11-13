package query

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
)

// A co-change partner in a declared data path isn't listed; the check says it left
// one out.
func TestCoChangeCheck_LeavesOutDeclaredDataPaths(t *testing.T) {
	engine, root := coChangeRepo(t)
	engine.config.Paths.Data = []string{"cv/importer.go"}
	editRepoFile(t, root, "cv/editor.go", "package cv\n", "package cv\n\ntype CV struct{ Title string }\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).CoChangeCheck
	if check == nil || check.Status != CoChangeClear || len(check.OpenFiles) != 0 {
		t.Fatalf("check = %+v, want clear: the only partner is data", check)
	}
	if check.LeftOut == nil || check.LeftOut.Count != 1 || !strings.Contains(check.Note, "1 file in data/generated paths left out") {
		t.Errorf("leftOut = %+v, note = %q", check.LeftOut, check.Note)
	}
}

// Benchmark data committed in batches outranked the scripts; declared as data it
// drops out and the response counts it.
func TestGetHotspots_LeavesOutDeclaredDataPaths(t *testing.T) {
	engine, root := doneCheckRepo(t)
	gitAdapter, err := git.NewGitAdapter(engine.config, engine.logger) // The engine came before the repo
	if err != nil {
		t.Fatal(err)
	}
	engine.gitAdapter = gitAdapter
	writeRepoFile(t, root, "bench.py", "x = 0\n")
	for j := 0; j < 6; j++ {
		writeRepoFile(t, root, fmt.Sprintf("bench-data/ocr/page%d.json", j), "{}\n")
	}
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "data")
	for i := 0; i < 3; i++ {
		paths := []string{"bench.py"}
		for j := 0; j < 6; j++ {
			paths = append(paths, fmt.Sprintf("bench-data/ocr/page%d.json", j))
		}
		commitTouching(t, root, fmt.Sprint("run ", i), paths...)
	}

	engine.config.Paths.Data = []string{"bench-data/**"}
	resp, err := engine.GetHotspots(context.Background(), GetHotspotsOptions{Limit: 20, SkipComplexity: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range resp.Hotspots {
		if strings.HasPrefix(h.FilePath, "bench-data/") {
			t.Errorf("data file ranked: %s", h.FilePath)
		}
	}
	if resp.LeftOut == nil || resp.LeftOut.Count != 6 || resp.LeftOut.ByClass["data"] != 6 {
		t.Fatalf("leftOut = %+v, want 6 data files", resp.LeftOut)
	}
	said := false
	for _, l := range resp.Limitations {
		said = said || strings.HasPrefix(l, "6 files in data/generated paths left out")
	}
	if !said {
		t.Errorf("limitations %q don't say what was left out", resp.Limitations)
	}
}
