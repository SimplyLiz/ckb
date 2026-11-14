package perf

import (
	"testing"
)

// ─── Pure function tests ──────────────────────────────────────────────────────

func TestCorrelationLevel(t *testing.T) {
	tests := []struct {
		corr float64
		want string
	}{
		{1.0, "high"},
		{5.0 / 7, "high"}, // 5 of 5 shared commits
		{0.69, "medium"},
		{0.5, "medium"},
		{0.49, "low"},
		{0.3, "low"},
		{0.0, "low"},
	}
	for _, tt := range tests {
		got := CorrelationLevel(tt.corr)
		if got != tt.want {
			t.Errorf("CorrelationLevel(%v) = %q, want %q", tt.corr, got, tt.want)
		}
	}
}

func TestShouldIgnore(t *testing.T) {
	yes := []string{
		"testdata/fixtures/go/foo.go",
		"testdata/file.go",
		"vendor/github.com/foo/bar.go",
		"node_modules/lodash/index.js",
		".ckb/config.json",
	}
	no := []string{
		"internal/perf/analyzer.go",
		"cmd/ckb/main.go",
		"docs/README.md",
		"testable_code.go", // starts with "test" but not "testdata/"
	}
	for _, p := range yes {
		if !shouldIgnore(p) {
			t.Errorf("shouldIgnore(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if shouldIgnore(p) {
			t.Errorf("shouldIgnore(%q) = true, want false", p)
		}
	}
}

// ─── recordCommit unit tests ─────────────────────────────────────────────────

func TestRecordCommit_Empty(t *testing.T) {
	a := &Analyzer{}
	pairs := make(map[filePair]int)
	totals := make(map[string]int)
	a.recordCommit(nil, pairs, totals, make(map[string]bool))
	if len(pairs) != 0 || len(totals) != 0 {
		t.Error("empty files should produce no pairs or totals")
	}
}

func TestRecordCommit_SingleFile(t *testing.T) {
	a := &Analyzer{}
	pairs := make(map[filePair]int)
	totals := make(map[string]int)
	a.recordCommit([]string{"a.go"}, pairs, totals, make(map[string]bool))
	if len(pairs) != 0 {
		t.Errorf("single file should produce no pairs, got %d", len(pairs))
	}
	if totals["a.go"] != 1 {
		t.Errorf("total for a.go = %d, want 1", totals["a.go"])
	}
}

func TestRecordCommit_TwoFiles(t *testing.T) {
	a := &Analyzer{}
	pairs := make(map[filePair]int)
	totals := make(map[string]int)
	a.recordCommit([]string{"a.go", "b.go"}, pairs, totals, make(map[string]bool))
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	// Pair key must be ordered (a <= b).
	key := filePair{"a.go", "b.go"}
	if pairs[key] != 1 {
		t.Errorf("pair count = %d, want 1", pairs[key])
	}
}

func TestRecordCommit_Deduplication(t *testing.T) {
	// Same file listed twice in a commit should only count once.
	a := &Analyzer{}
	pairs := make(map[filePair]int)
	totals := make(map[string]int)
	a.recordCommit([]string{"a.go", "a.go", "b.go"}, pairs, totals, make(map[string]bool))
	if totals["a.go"] != 1 {
		t.Errorf("a.go total = %d, want 1 (dedup within commit)", totals["a.go"])
	}
	if len(pairs) != 1 {
		t.Errorf("expected 1 pair after dedup, got %d", len(pairs))
	}
}

func TestRecordCommit_OrderedKey(t *testing.T) {
	// Regardless of input order, the pair key must have a <= b.
	a := &Analyzer{}
	pairs := make(map[filePair]int)
	totals := make(map[string]int)
	a.recordCommit([]string{"z.go", "a.go"}, pairs, totals, make(map[string]bool))
	key := filePair{"a.go", "z.go"}
	if pairs[key] != 1 {
		t.Errorf("pair not found with ordered key %v", key)
	}
}

func TestRecordCommit_IgnoredPaths(t *testing.T) {
	// testdata/ files should be silently dropped.
	a := &Analyzer{}
	pairs := make(map[filePair]int)
	totals := make(map[string]int)
	a.recordCommit([]string{"testdata/foo.go", "internal/bar.go"}, pairs, totals, make(map[string]bool))
	if _, ok := totals["testdata/foo.go"]; ok {
		t.Error("testdata file should be ignored")
	}
	if len(pairs) != 0 {
		t.Error("pair with ignored file should not appear")
	}
}

func TestRecordCommit_ThreeFiles(t *testing.T) {
	a := &Analyzer{}
	pairs := make(map[filePair]int)
	totals := make(map[string]int)
	a.recordCommit([]string{"a.go", "b.go", "c.go"}, pairs, totals, make(map[string]bool))
	// 3 files → 3 pairs: (a,b), (a,c), (b,c)
	if len(pairs) != 3 {
		t.Errorf("expected 3 pairs for 3 files, got %d", len(pairs))
	}
}

// ─── ScanOptions defaults test ────────────────────────────────────────────────
