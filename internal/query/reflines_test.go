package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// References come out the way grep prints them, with the line of code and what the
// line does with the symbol, so the file doesn't have to be opened to tell a call
// from an import.
func TestReferenceLines(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"lib/kind.ts":     "export type Kind = \"a\" | \"b\";\nexport function show(kind: Kind): string {\n  return kind;\n}\nexport let current: Kind = \"a\";\n",
		"app/use.ts":      "import { show, current } from \"../lib/kind\";\nimport type { Kind } from \"../lib/kind\";\n\nconst label = show(\"a\");\ncurrent = \"b\";\nconst k: Kind = \"a\";\nconsole.log(current);\n",
		"app/use.test.ts": "import { show } from \"../lib/kind\";\n\nit(\"shows\", () => {\n  expect(show(\"a\")).toBe(\"a\");\n});\n",
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at := func(file string, line int, kind, from string, test bool) ReferenceInfo {
		return ReferenceInfo{Location: &LocationInfo{FileId: file, StartLine: line}, Kind: kind, From: from, IsTest: test}
	}

	show := BuildReferenceLines(root, "scip-typescript npm m 1.0.0 lib/`kind.ts`/show().", &FindReferencesResponse{
		TotalCount: 4,
		References: []ReferenceInfo{
			at("lib/kind.ts", 2, "definition", "", false),
			at("app/use.ts", 1, "read", "", false),
			at("app/use.ts", 4, "read", "label", false),
			at("app/use.test.ts", 1, "read", "", true),
			at("app/use.test.ts", 4, "read", "shows", true),
		},
	})
	want := `lib/kind.ts:2: [def] export function show(kind: Kind): string {
app/use.ts:4: [call] const label = show("a");
app/use.test.ts:4: [call] expect(show("a")).toBe("a");
show: 4 references, 1 in tests; 2 imports not listed
`
	if got := show.Text(false); got != want {
		t.Errorf("text =\n%s\nwant\n%s", got, want)
	}
	grouped := show.Text(true)
	if !strings.Contains(grouped, "label\n  app/use.ts:4: [call] const label = show(\"a\");\n") || !strings.Contains(grouped, "shows (test)\n  app/use.test.ts:4:") {
		t.Errorf("grouped by enclosing symbol =\n%s", grouped)
	}

	current := BuildReferenceLines(root, "scip-typescript npm m 1.0.0 lib/`kind.ts`/current.", &FindReferencesResponse{
		TotalCount: 3,
		References: []ReferenceInfo{at("app/use.ts", 1, "read", "", false), at("app/use.ts", 5, "read", "", false), at("app/use.ts", 7, "read", "", false)},
	})
	if got := current.Text(false); !strings.Contains(got, "app/use.ts:5: [write] current = \"b\";") || !strings.Contains(got, "app/use.ts:7: [read] console.log(current);") {
		t.Errorf("write and read =\n%s", got)
	}

	// A file that changed since the index was built: the line it points at is something else now.
	moved := BuildReferenceLines(root, "scip-typescript npm m 1.0.0 lib/`kind.ts`/show().", &FindReferencesResponse{
		TotalCount: 1, References: []ReferenceInfo{at("app/use.ts", 3, "read", "", false)},
	})
	if got := moved.Text(false); !strings.Contains(got, "app/use.ts:3: [stale] \n") || !strings.Contains(got, "1 marked stale no longer name it") {
		t.Errorf("stale line =\n%s", got)
	}

	kind := BuildReferenceLines(root, "scip-typescript npm m 1.0.0 lib/`kind.ts`/Kind#", &FindReferencesResponse{
		TotalCount: 3, Truncated: true,
		References: []ReferenceInfo{at("lib/kind.ts", 2, "read", "show", false), at("app/use.ts", 2, "read", "", false), at("app/use.ts", 6, "read", "", false)},
	})
	got := kind.Text(false)
	if !strings.Contains(got, "lib/kind.ts:2: [type] export function show(kind: Kind): string {") || !strings.Contains(got, "app/use.ts:6: [type] const k: Kind = \"a\";") {
		t.Errorf("type uses =\n%s", got)
	}
	if !strings.Contains(got, "Kind: 3 references; 1 import not listed; the list is cut, raise the limit for the rest") {
		t.Errorf("summary =\n%s", got)
	}
}
