package secrets

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// A URL without userinfo followed, later on the same shell line, by an '@'
// (here "$@") is not a password in a URL. The userinfo part of a URL holds no
// whitespace or quotes, so a match must not cross either.
func TestPasswordInURLDoesNotCrossWhitespace(t *testing.T) {
	lines := []string{
		`curl -s http://127.0.0.1:8081/v1/chat/completions -H "Content-Type: application/json" "$@"`,
		`exec python bench.py --url http://localhost:8080/v1 --model qwen "$@"`,
		`echo "server at http://lisa-linux.local:8080 ready, mail admin@example.org"`,
		`mongodb://localhost:27017/db then notify ops@example.org`,
		`postgres://db.internal:5432/app # owner: someone@example.org`,
		`redis://cache.internal:6379/0 'x' user@host`,
		`mysql://db.internal:3306/app "y" root@host`,
	}
	for _, line := range lines {
		for _, p := range BuiltinPatterns {
			if loc := p.Regex.FindStringIndex(line); loc != nil {
				t.Errorf("%s matched %q in %q", p.Name, line[loc[0]:loc[1]], line)
			}
		}
	}
}

// The tightened patterns still find real credentials in URLs.
func TestPasswordInURLStillMatchesUserinfo(t *testing.T) {
	cases := map[string]string{
		"password_in_url": `curl https://deploy:Zq8vR2mXk4Lp@registry.internal/v2/ "$@"`,
		"mongodb_uri":     `mongodb+srv://admin:Zq8vR2mXk4Lp@cluster0.mongodb.net/app`,
		"postgres_uri":    `DATABASE_URL="postgresql://svc:Zq8vR2:mXk4Lp@db.internal:5432/app"`,
		"redis_uri":       `redis://:Zq8vR2mXk4Lp@cache.internal:6379/0`,
		"mysql_uri":       `mysql://root:Zq8vR2mXk4Lp@db.internal:3306/app`,
	}
	for rule, line := range cases {
		p := GetPatternByName(rule)
		if p == nil {
			t.Fatalf("no pattern %s", rule)
		}
		m := p.Regex.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("%s: no match in %q", rule, line)
			continue
		}
		if len(m) > 1 && (m[1] != "Zq8vR2mXk4Lp" && m[1] != "Zq8vR2:mXk4Lp") {
			t.Errorf("%s: captured %q", rule, m[1])
		}
	}
}

// End to end: the shell script shape from the report produces no finding.
func TestScanShellScriptLocalURLAndArgs(t *testing.T) {
	dir := t.TempDir()
	script := "#!/usr/bin/env bash\nset -euo pipefail\n" +
		`curl -sS http://127.0.0.1:8081/v1/completions -H "Authorization: none" -d @payload.json "$@"` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "bench.sh"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := NewScanner(dir, slog.Default()).Scan(context.Background(), ScanOptions{RepoRoot: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Findings {
		t.Errorf("unexpected finding %s at %s:%d: %s", f.Rule, f.File, f.Line, f.Context)
	}
}
