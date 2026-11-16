package overlay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// A file in a directory the index has no package in gets its package symbol
// from go.mod and the module's version field in the index.
func TestGoNewPackagePrefix(t *testing.T) {
	root := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.22\n")
	write("tools/go.mod", "module tools\n\ngo 1.22\n")
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"lib/lib.go": goOldDoc()})

	cases := []struct{ path, pkg, want string }{
		{"lib/sub/x.go", "sub", "scip-go gomod example.com/m v1 `example.com/m/lib/sub`/"},
		{"cmd/app/main.go", "main", "scip-go gomod example.com/m v1 `example.com/m/cmd/app`/"},
		{"lib/sub/x_test.go", "sub_test", ""}, // external test package
		{"tools/gen/gen.go", "gen", ""},       // module the index has no package of
	}
	for _, c := range cases {
		if got := goNewPackagePrefix(base, root, c.path, c.pkg); got != c.want {
			t.Errorf("%s: got %q, want %q", c.path, got, c.want)
		}
	}

	write("lib/sub/x.go", "package sub\n\nfunc New() int { return 1 }\n")
	doc, prefix := goNewDocument(base, root, "lib/sub/x.go", []byte("package sub\n\nfunc New() int { return 1 }\n"))
	if doc == nil || prefix != cases[0].want {
		t.Fatalf("goNewDocument: doc %v, prefix %q", doc != nil, prefix)
	}
	found := false
	for _, o := range doc.Occurrences {
		if o.Symbol == prefix+"New()." && o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("New() not defined in %+v", doc.Occurrences)
	}

	if got, want := scipDescriptorName("simple_1"), "simple_1"; got != want {
		t.Errorf("simple name escaped: %q", got)
	}
	if got, want := scipDescriptorName("a/b`c"), "`a/b``c`"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
