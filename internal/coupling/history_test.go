package coupling

import (
	"math"
	"strings"
	"testing"
)

func TestScoreMatchesCartographer(t *testing.T) {
	cases := []struct {
		count, a, b int
		want        float64
	}{
		{2, 2, 40, 0.5},
		{5, 5, 9, 5.0 / 7},
		{14, 16, 30, 14.0 / 18},
		{10, 10, 10, 10.0 / 12},
		{3, 1, 1, 3.0 / 5}, // count above churn: churn is at least count
	}
	for _, c := range cases {
		if got := Score(c.count, c.a, c.b); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Score(%d, %d, %d) = %v, want %v", c.count, c.a, c.b, got, c.want)
		}
	}
}

func TestParseHistoryLog(t *testing.T) {
	big := make([]string, maxFilesPerCommit+1)
	for i := range big {
		big[i] = "gen/f" + string(rune('a'+i%26)) + ".go"
	}
	log := strings.Join([]string{
		"\x1fLisa\x1ffeat: one",
		"src/a.ts",
		"src/b.ts",
		"pnpm-lock.yaml",
		"logo.png",
		"third_party/lib/src/api.rs",
		"web/dist/app.js",
		"",
		"\x1fdependabot[bot]\x1fbump",
		"src/a.ts",
		"package.json",
		"",
		"\x1fLisa\x1frun prettier",
		"src/a.ts",
		"src/c.ts",
		"",
		"\x1fLisa\x1fsweep",
		strings.Join(big, "\n"),
		"",
		"\x1fLisa\x1ffix: two",
		"src/a.ts",
		"src/b.ts",
		"",
	}, "\n")
	commits := parseHistoryLog(log, nil)
	if len(commits) != 2 {
		t.Fatalf("commits = %v, want the two feature commits (bot, formatting and sweep dropped)", commits)
	}
	for _, c := range commits {
		if len(c) != 2 {
			t.Errorf("commit %v: lockfile and image should be dropped", c)
		}
	}
	h := newHistory(commits)
	p := h.Partners("src/a.ts")
	if len(p) != 1 || p[0].FileB != "src/b.ts" || p[0].Count != 2 || h.Churn("src/a.ts") != 2 {
		t.Errorf("partners = %+v churn = %d", p, h.Churn("src/a.ts"))
	}
}

func TestStrongPartnersOrderAndFloors(t *testing.T) {
	var commits [][]string
	add := func(n int, files ...string) {
		for i := 0; i < n; i++ {
			commits = append(commits, files)
		}
	}
	add(2, "x", "two")     // 2 of 2: score 0.5, under the count floor
	add(14, "x", "strong") // strong also changes alone twice
	add(2, "strong")
	add(5, "x", "five")
	add(20, "x") // x is busy
	h := newHistory(commits)
	got := h.StrongPartners("x", 3, 0.3, 0)
	if len(got) != 2 || got[0].FileB != "strong" || got[1].FileB != "five" {
		t.Fatalf("got %+v, want strong then five", got)
	}
	if got := h.StrongPartners("x", 3, 0.3, 1); len(got) != 1 {
		t.Errorf("limit 1 gave %d", len(got))
	}
	// Seen from the less-changed side the score is the same: it is symmetric.
	if a, b := h.StrongPartners("five", 3, 0, 0), got; len(a) != 1 || a[0].Score != h.StrongPartners("x", 3, 0, 0)[1].Score || len(b) == 0 {
		t.Errorf("score should be symmetric: %+v", a)
	}
}

func TestHistoryAllPairs(t *testing.T) {
	h := HistoryFromCommits([][]string{
		{"b.go", "a.go", "c.go"},
		{"a.go", "b.go"},
		{"a.go", "b.go"},
		{"c.go"},
	})
	got := h.AllPairs(2)
	if len(got) != 1 {
		t.Fatalf("AllPairs(2) = %+v, want only a.go/b.go", got)
	}
	p := got[0]
	if p.FileA != "a.go" || p.FileB != "b.go" || p.Count != 3 || math.Abs(p.Score-3.0/5) > 1e-9 {
		t.Errorf("pair = %+v, want a.go/b.go, 3 commits, score 0.6", p)
	}
	if all := h.AllPairs(1); len(all) != 3 || all[0].FileB != "b.go" {
		t.Errorf("AllPairs(1) = %+v, want 3 pairs, a.go/b.go first", all)
	}
	if h.Commits() != 4 || h.Files() != 3 {
		t.Errorf("Commits, Files = %d, %d, want 4, 3", h.Commits(), h.Files())
	}
	var nilH *History
	if nilH.AllPairs(1) != nil || nilH.Commits() != 0 || nilH.Files() != 0 {
		t.Error("nil History must be empty")
	}
}
