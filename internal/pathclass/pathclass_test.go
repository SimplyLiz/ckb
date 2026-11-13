package pathclass

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"bench-data/**", "bench-data/ocr/page1.json", true},
		{"bench-data/**", "src/bench-data/x.json", false},
		{"bench-data", "bench-data/x.json", true},
		{"bench-data/", "bench-data/a/b.csv", true},
		{"bench-data", "bench-database/x.json", false},
		{"*.pb.go", "api/v1/service.pb.go", true},
		{"*.pb.go", "api/v1/service.go", false},
		{"**/dist/*.js", "web/app/dist/index.js", true},
		{"**/dist/*.js", "web/app/src/index.js", false},
		{"data/**/*.csv", "data/2026/run/a.csv", true},
		{"data/**/*.csv", "data/2026/run/a.json", false},
		{"*__generated__*", "src/__generated__/types.ts", true},
		// Three-part patterns anchor both ends.
		{"gen*/*.ts", "gen/x.ts", true},
		{"gen*/*.ts", "src/gen/x.tsx", false},
		{"*.min.*", "a.min.js", true},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "exports", "dump.json")
	if err := os.MkdirAll(filepath.Dir(big), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, []byte(strings.Repeat("x", LargeDataBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(dir, []string{"./gen/**"}, []string{"bench-data/**"})
	cases := []struct {
		path string
		want Class
	}{
		{"bench-data/ocr/page1.json", Data},
		{"gen/client.ts", Generated},
		{"api/service.pb.go", Generated}, // built-in default
		{"exports/dump.json", Data},      // large data file
		{"package.json", None},           // small config stays
		{"internal/query/engine.go", None},
		{"missing/huge.json", None}, // not on disk: no size, no class
	}
	for _, tc := range cases {
		if got, _ := c.Classify(tc.path); got != tc.want {
			t.Errorf("Classify(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}

	// Without a repo root the size rule is off.
	if got, _ := New("", nil, nil).Classify("exports/dump.json"); got != None {
		t.Errorf("pattern-only classifier put exports/dump.json in %q", got)
	}
}

func TestCounterNote(t *testing.T) {
	var n Counter
	if n.Tally() != nil || n.Note() != "" {
		t.Fatal("empty counter should say nothing")
	}
	c := New("", nil, []string{"bench-data/**"})
	for _, p := range []string{"bench-data/a.json", "bench-data/a.json", "bench-data/b.json", "x.pb.go", "main.go"} {
		n.Skip(c, p)
	}
	tally := n.Tally()
	if tally.Count != 3 || tally.ByClass["data"] != 2 || tally.ByClass["generated"] != 1 {
		t.Fatalf("tally = %+v", tally)
	}
	if !strings.HasPrefix(tally.Note, "3 files in data/generated paths left out (data 2, generated 1") {
		t.Errorf("note = %q", tally.Note)
	}
	if len(tally.Examples) != 3 || tally.Examples[0] != "bench-data/a.json" {
		t.Errorf("examples = %v", tally.Examples)
	}
}

func TestLoadReadsConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"paths": {"generated": ["out/**"], "data": ["bench-data"]}, "review": {"generatedPatterns": ["*.gen.ts"]}}`
	if err := os.WriteFile(filepath.Join(dir, ".ckb", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Load(dir)
	for path, want := range map[string]Class{
		"bench-data/x.csv": Data,
		"out/a.js":         Generated,
		"src/api.gen.ts":   Generated, // review.generatedPatterns counts too
		"src/api.ts":       None,
	} {
		if got, _ := c.Classify(path); got != want {
			t.Errorf("Classify(%q) = %q, want %q", path, got, want)
		}
	}
}
