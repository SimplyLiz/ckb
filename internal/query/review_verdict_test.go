package query

import (
	"strings"
	"testing"
)

func TestExplainVerdict(t *testing.T) {
	policy := DefaultReviewPolicy()
	pass := []ReviewCheck{{Name: "tests", Status: "pass"}, {Name: "hotspots", Status: "info"}}

	cases := []struct {
		name     string
		checks   []ReviewCheck
		findings []ReviewFinding
		score    int
		verdict  string
		reason   string
	}{
		{"clean", pass, nil, 95, "pass", "no check warned"},
		{"warned check", []ReviewCheck{{Name: "risk", Status: "warn"}}, nil, 90, "warn", "warned: risk"},
		{"failed check", []ReviewCheck{{Name: "secrets", Status: "fail"}, {Name: "risk", Status: "warn"}}, nil, 90, "fail", "failed: secrets"},
		// The 9.3 case: every check passed and the score sat on its floor.
		{"pass at the floor", pass, []ReviewFinding{{Severity: "info"}}, 20, "warn", "score 20 is below 50"},
		{"warning in a passing check", pass, []ReviewFinding{{Severity: "warning"}}, 97, "warn", "1 warning or error finding(s) in passing checks"},
		{"info findings alone", pass, []ReviewFinding{{Severity: "info"}, {Severity: "info"}}, 98, "pass", "no check warned"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, r := explainVerdict(c.checks, c.findings, c.score, policy)
			if v != c.verdict || !strings.Contains(r, c.reason) {
				t.Errorf("got %q (%s), want %q (%s)", v, r, c.verdict, c.reason)
			}
		})
	}

	strict := DefaultReviewPolicy()
	strict.FailOnLevel = "warning"
	if v, _ := explainVerdict(pass, nil, 30, strict); v != "fail" {
		t.Errorf("failOnLevel=warning with a low score: got %q, want fail", v)
	}
	none := DefaultReviewPolicy()
	none.FailOnLevel = "none"
	if v, _ := explainVerdict([]ReviewCheck{{Name: "x", Status: "fail"}}, nil, 20, none); v != "pass" {
		t.Errorf("failOnLevel=none: got %q, want pass", v)
	}
}

func TestDynamicAccessLimitation(t *testing.T) {
	diff := `diff --git a/src/lib/cv/entries.ts b/src/lib/cv/entries.ts
--- a/src/lib/cv/entries.ts
+++ b/src/lib/cv/entries.ts
@@ -40,3 +40,7 @@ export function setList(
 export function setList(profile: Profile, kind: ListKind, list: unknown[]) {
-  old[kind] = list;
+  (profile[kind] as { id: string }[]) = list;
+  const first = list[i];
+  type Item = Profile[K][number];
+  // profile[kind] in a comment
+  const words = WORDS[kind];
diff --git a/src/main.go b/src/main.go
--- a/src/main.go
+++ b/src/main.go
@@ -1,1 +1,2 @@
+	v := m[key]
diff --git a/tests/x.test.ts b/tests/x.test.ts
--- a/tests/x.test.ts
+++ b/tests/x.test.ts
@@ -1,1 +1,2 @@
+  expect(profile[kind]).toBe(1)
`
	l := dynamicAccessLimitation(diff, 5)
	if l == nil {
		t.Fatal("no limitation")
	}
	want := []string{
		"src/lib/cv/entries.ts:41: (profile[kind] as { id: string }[]) = list;",
		"src/lib/cv/entries.ts:45: const words = WORDS[kind];",
	}
	if len(l.Examples) != len(want) {
		t.Fatalf("examples = %q, want %q", l.Examples, want)
	}
	for i := range want {
		if l.Examples[i] != want[i] {
			t.Errorf("example %d = %q, want %q", i, l.Examples[i], want[i])
		}
	}
	if !strings.HasPrefix(l.Message, "2 added line(s)") {
		t.Errorf("message = %q", l.Message)
	}
	if dynamicAccessLimitation("+++ b/a.ts\n@@ -1 +1 @@\n+const x = y.z;\n", 5) != nil {
		t.Error("a plain member access is no computed key")
	}
}
