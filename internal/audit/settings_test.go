package audit

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIdentifierParts(t *testing.T) {
	cases := map[string][]string{
		"getAccessToken": {"get", "access", "token"},
		"ACCESS_TOKENS":  {"access", "token"},
		"HTTPServer":     {"http", "server"},
		"api-key":        {"api", "key"},
		"credentials":    {"credential"},
		"access":         {"access"},
	}
	for in, want := range cases {
		if got := identifierParts(in); !reflect.DeepEqual(got, want) {
			t.Errorf("identifierParts(%q) = %v, want %v", in, got, want)
		}
	}
}

// The default keywords find credentials in code and leave an LLM benchmark
// script's tokens, keys and sessions alone.
func TestDefaultSecurityKeywordsMatching(t *testing.T) {
	kws := compileKeywords(SecurityKeywords)
	quiet := `import requests
from transformers import AutoTokenizer
tokenizer = AutoTokenizer.from_pretrained(model)
max_tokens = 512
eos_token_id = tokenizer.eos_token_id
for key, value in results.items():
    cache_key = hash(prompt)
session = requests.Session()
# author: someone
private int count; // Java
print(f"{tokens_per_second:.1f} tok/s")
`
	if got := matchSecurityKeywords(quiet, kws); len(got) != 0 {
		t.Errorf("benchmark code flagged: %v", got)
	}

	loud := map[string]string{
		`def login(user, password):`:               "password",
		`API_KEY = os.environ["OPENAI_API_KEY"]`:   "api_key",
		`const accessToken = await refresh();`:     "access_token",
		`headers["Authorization"] = "Bearer " + t`: "authorization",
		`HF_TOKEN = os.getenv("HF_TOKEN")`:         "hf_token",
		`claims = jwt.decode(raw, verify=True)`:    "jwt",
		`client_secret: str`:                       "secret",
		`func authMiddleware(next http.Handler)`:   "auth",
		`hashed := bcrypt.GenerateFromPassword(p)`: "bcrypt",
	}
	for code, want := range loud {
		got := matchSecurityKeywords(code, kws)
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: got %v, want %q among them", code, got, want)
		}
	}
}

func writeCKBConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ckb", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func factorOf(item *RiskItem, name string) RiskFactor {
	for _, f := range item.Factors {
		if f.Factor == name {
			return f
		}
	}
	return RiskFactor{}
}

func TestAuditConfigKeywordsAndDisabledFactors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	src := "max_tokens = 10\nnonce = make_nonce()\n"

	// Own keyword list replaces the defaults.
	dir := t.TempDir()
	writeCKBConfig(t, dir, `{"version":5,"audit":{"securityKeywords":["nonce"]}}`)
	if err := os.WriteFile(filepath.Join(dir, "run.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := NewAnalyzer(dir, logger).analyzeFile(context.Background(), dir, "run.py")
	if err != nil {
		t.Fatal(err)
	}
	if f := factorOf(item, FactorSecuritySensitive); f.Contribution == 0 || f.Value != "nonce" {
		t.Errorf("custom keyword: %+v", f)
	}

	// A disabled factor is listed and scores nothing.
	dir2 := t.TempDir()
	writeCKBConfig(t, dir2, `{"version":5,"audit":{"securityKeywords":["nonce"],"disabledFactors":["security_sensitive"]}}`)
	if err := os.WriteFile(filepath.Join(dir2, "run.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err = NewAnalyzer(dir2, logger).analyzeFile(context.Background(), dir2, "run.py")
	if err != nil {
		t.Fatal(err)
	}
	if f := factorOf(item, FactorSecuritySensitive); f.Contribution != 0 || !strings.Contains(f.Value, "disabled") {
		t.Errorf("disabled factor: %+v", f)
	}
}

func gitRun(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func author(name string) []string {
	return []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + name + "@x",
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + name + "@x"}
}

// In a repo with one author, bus factor 1 is everywhere: it isn't scored and
// produces no "Assign backup owner". With a second author it is again.
func TestBusFactorSingleAuthorRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	gitRun(t, dir, nil, "init", "-q")
	for _, f := range []string{"a.py", "b.py"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("print(1)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, dir, author("lisa"), "add", "a.py")
	gitRun(t, dir, author("lisa"), "commit", "-q", "-m", "a")
	gitRun(t, dir, author("dependabot[bot]"), "add", "b.py")
	gitRun(t, dir, author("dependabot[bot]"), "commit", "-q", "-m", "b")

	a := NewAnalyzer(dir, logger)
	item, err := a.analyzeFile(context.Background(), dir, "a.py")
	if err != nil {
		t.Fatal(err)
	}
	f := factorOf(item, FactorBusFactor)
	if f.Contribution != 0 || !strings.Contains(f.Value, "single-author") {
		t.Errorf("single-author repo bus factor: %+v", f)
	}
	for _, w := range a.findQuickWins([]RiskItem{*item}) {
		if w.Action == "Assign backup owner" {
			t.Errorf("quick win %+v in a single-author repo", w)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "c.py"), []byte("print(2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, author("sam"), "add", "c.py")
	gitRun(t, dir, author("sam"), "commit", "-q", "-m", "c")
	item, err = NewAnalyzer(dir, logger).analyzeFile(context.Background(), dir, "a.py")
	if err != nil {
		t.Fatal(err)
	}
	if f := factorOf(item, FactorBusFactor); f.Contribution != 15 {
		t.Errorf("two-author repo, one-author file: %+v", f)
	}
}
