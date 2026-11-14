package secrets

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func findingsByFile(res *ScanResult) map[string][]string {
	out := make(map[string][]string)
	for _, f := range res.Findings {
		out[f.File] = append(out[f.File], f.Rule)
	}
	return out
}

func TestInlineAllowMarkers(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.sh": "U=" + pgURL() + " # ckb:allow-secret\n",
		"b.sh": "U=" + pgURL() + " # gitleaks:allow\n",
		"c.sh": "U=" + pgURL() + "\n",
	})
	res, err := NewScanner(dir, slog.Default()).Scan(context.Background(), ScanOptions{RepoRoot: dir, ApplyAllowlist: true})
	if err != nil {
		t.Fatal(err)
	}
	got := findingsByFile(res)
	if len(got["a.sh"]) != 0 || len(got["b.sh"]) != 0 {
		t.Errorf("marked lines reported: %v", got)
	}
	if len(got["c.sh"]) == 0 {
		t.Errorf("unmarked line not reported: %v", got)
	}
	if res.Suppressed < 2 {
		t.Errorf("suppressed %d, want the marked findings counted", res.Suppressed)
	}

	// --no-allowlist shows everything, inline markers included.
	res, err = NewScanner(dir, slog.Default()).Scan(context.Background(), ScanOptions{RepoRoot: dir, ApplyAllowlist: false})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsByFile(res); len(got["a.sh"]) == 0 || len(got["b.sh"]) == 0 {
		t.Errorf("without allowlist, marked lines hidden: %v", got)
	}
}

func TestInlineAllowInStagedDiff(t *testing.T) {
	g := NewGitScanner(t.TempDir())
	diff := "diff --git a/x.sh b/x.sh\n+++ b/x.sh\n@@ -0,0 +1,1 @@\n+U=" + pgURL() + " # ckb:allow-secret\n"
	findings, err := g.scanDiff(diff, "staged")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 || !findings[0].allowedInline {
		t.Errorf("staged finding not marked inline-allowed: %+v", findings)
	}
}

// A path entry with rules applies to those rules only: scripts/ can be
// allowed for password_in_url without hiding a real key there.
func TestAllowlistEntryScopedToRules(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		".ckb/secrets-allowlist.json": `{"version":"1.0","entries":[
			{"id":"bench-urls","type":"path","value":"scripts/**","rules":["postgres_uri","password_in_url"],"reason":"local bench URLs"}
		]}`,
		"scripts/sub/run.sh": "U=" + pgURL() + "\n" + "K=" + stripeTestLive() + "\n",
		"app/db.py":          "U = '" + pgURL() + "'\n",
	})
	res, err := NewScanner(dir, slog.Default()).Scan(context.Background(), ScanOptions{RepoRoot: dir, ApplyAllowlist: true})
	if err != nil {
		t.Fatal(err)
	}
	got := findingsByFile(res)
	for _, r := range got["scripts/sub/run.sh"] {
		if r == "postgres_uri" || r == "password_in_url" {
			t.Errorf("rule %s not allowed under scripts/**", r)
		}
	}
	if len(got["scripts/sub/run.sh"]) == 0 {
		t.Errorf("stripe key under scripts/ hidden by an entry scoped to URL rules: %v", got)
	}
	if len(got["app/db.py"]) == 0 {
		t.Errorf("finding outside scripts/ hidden: %v", got)
	}
}

func TestMatchGlobSegments(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"scripts/**", "scripts/a/b.sh", true},
		{"**/*.sh", "scripts/a/b.sh", true},
		{"bench-data/**", "bench-data/run1/out.json", true},
		{"scripts/**", "src/scripts.go", false},
		{"*.sh", "a/b.sh", false},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}
