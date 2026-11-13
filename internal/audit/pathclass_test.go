package audit

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
)

// Files in declared generated or data paths aren't scored, the result counts them,
// and co_change_coupling leaves data partners out and says so.
func TestAnalyzeLeavesOutDeclaredPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".ckb/config.json", `{"paths": {"generated": ["gen/**"], "data": ["bench-data/**"]}}`)
	git("init", "-q")
	for i := 0; i < 3; i++ {
		write("bench.py", fmt.Sprintf("x = %d\n", i))
		write("gen/client.py", fmt.Sprintf("y = %d\n", i))
		for j := 0; j < 4; j++ {
			write(fmt.Sprintf("bench-data/run%d.json", j), fmt.Sprint(i))
		}
		git("add", "bench.py", "gen", "bench-data")
		git("commit", "-q", "-m", fmt.Sprint(i))
	}

	a := NewAnalyzer(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	res, err := a.Analyze(context.Background(), AuditOptions{RepoRoot: dir, MinScore: 0.001})
	if err != nil {
		t.Fatal(err)
	}
	if res.LeftOut == nil || res.LeftOut.Count != 1 || res.LeftOut.ByClass["generated"] != 1 {
		t.Fatalf("LeftOut = %+v, want gen/client.py", res.LeftOut)
	}
	var bench *RiskItem
	for i := range res.Items {
		if strings.HasPrefix(res.Items[i].File, "gen/") {
			t.Errorf("generated file scored: %s", res.Items[i].File)
		}
		if res.Items[i].File == "bench.py" {
			bench = &res.Items[i]
		}
	}
	if bench == nil {
		t.Fatalf("bench.py not scored: %+v", res.Items)
	}
	for _, f := range bench.Factors {
		if f.Factor == FactorCoChangeCoupling && f.Value != "0 files (5 in data/generated paths left out)" {
			t.Errorf("co_change_coupling = %q", f.Value)
		}
	}
}
