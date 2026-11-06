package diff

import (
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

func lines(text string) []string { return strings.Split(text, "\n") }

func changed(blocks ...impact.ChangeBlock) *impact.ChangedFile {
	return &impact.ChangedFile{OldPath: "a.go", NewPath: "a.go", Hunks: []impact.ChangedHunk{{Blocks: blocks}}}
}

func TestClassifyUse(t *testing.T) {
	const name = "Save"
	tests := []struct {
		name       string
		file       *impact.ChangedFile
		old, new   string
		site       int
		state      UseState
		line       int
		code       string
		unverified bool
		isImport   bool
	}{
		{
			name: "a comment edit in the file leaves the call open",
			file: changed(impact.ChangeBlock{OldLine: 1, NewLine: 1, Removed: []string{"// old"}, Added: []string{"// new"}}),
			old:  "// old\nfunc f() {\n\tlib.Save(1)\n}",
			new:  "// new\nfunc f() {\n\tlib.Save(1)\n}",
			site: 3, state: UseOpen, line: 3, code: "lib.Save(1)",
		},
		{
			name: "a changed call is updated",
			file: changed(impact.ChangeBlock{OldLine: 2, NewLine: 2, Removed: []string{"\tlib.Save(1)"}, Added: []string{"\tlib.Save(1, 2)"}}),
			old:  "func f() {\n\tlib.Save(1)\n}",
			new:  "func f() {\n\tlib.Save(1, 2)\n}",
			site: 2, state: UseUpdated, line: 2,
		},
		{
			name: "a new argument on a following line updates the call",
			file: changed(impact.ChangeBlock{OldLine: 3, NewLine: 3, Added: []string{"\t\t2,"}}),
			old:  "lib.Save(\n\t\t1,\n\t)\nnext()",
			new:  "lib.Save(\n\t\t1,\n\t\t2,\n\t)\nnext()",
			site: 1, state: UseUpdated, line: 1,
		},
		{
			name: "a removed argument on a following line updates the call",
			file: changed(impact.ChangeBlock{OldLine: 3, NewLine: 3, Removed: []string{"\t\t2,"}}),
			old:  "lib.Save(\n\t\t1,\n\t\t2,\n\t)\nnext()",
			new:  "lib.Save(\n\t\t1,\n\t)\nnext()",
			site: 1, state: UseUpdated, line: 1,
		},
		{
			name: "a change after the call's closing line doesn't count",
			file: changed(impact.ChangeBlock{OldLine: 4, NewLine: 4, Removed: []string{"next()"}, Added: []string{"later()"}}),
			old:  "lib.Save(\n\t\t1,\n\t)\nnext()",
			new:  "lib.Save(\n\t\t1,\n\t)\nlater()",
			site: 1, state: UseOpen, line: 1, code: "lib.Save(",
		},
		{
			name: "a parenthesis in a string doesn't end the call",
			file: changed(impact.ChangeBlock{OldLine: 2, NewLine: 2, Removed: []string{"\t2)"}, Added: []string{"\t2, 3)"}}),
			old:  "lib.Save(\")\",\n\t2)",
			new:  "lib.Save(\")\",\n\t2, 3)",
			site: 1, state: UseUpdated, line: 1,
		},
		{
			name: "lines moved by an insertion above are followed",
			file: changed(impact.ChangeBlock{OldLine: 1, NewLine: 1, Added: []string{"// one", "// two"}}),
			old:  "func f() {\n\tlib.Save(1)\n}",
			new:  "// one\n// two\nfunc f() {\n\tlib.Save(1)\n}",
			site: 2, state: UseOpen, line: 4, code: "lib.Save(1)",
		},
		{
			name: "an index on the new side, comment edit: open",
			file: changed(impact.ChangeBlock{OldLine: 1, NewLine: 1, Added: []string{"// one", "// two"}}),
			old:  "func f() {\n\tlib.Save(1)\n}",
			new:  "// one\n// two\nfunc f() {\n\tlib.Save(1)\n}",
			site: 4, state: UseOpen, line: 4, code: "lib.Save(1)",
		},
		{
			name: "an index on the new side, changed call: updated",
			file: changed(
				impact.ChangeBlock{OldLine: 1, NewLine: 1, Added: []string{"// one", "// two"}},
				impact.ChangeBlock{OldLine: 2, NewLine: 4, Removed: []string{"\tlib.Save(1)"}, Added: []string{"\tlib.Save(1, 2)"}},
			),
			old:  "func f() {\n\tlib.Save(1)\n}",
			new:  "// one\n// two\nfunc f() {\n\tlib.Save(1, 2)\n}",
			site: 4, state: UseUpdated, line: 4,
		},
		{
			name: "a renamed file without edits leaves the call open",
			file: &impact.ChangedFile{OldPath: "old.go", NewPath: "new.go", Renamed: true},
			old:  "lib.Save(1)",
			new:  "lib.Save(1)",
			site: 1, state: UseOpen, line: 1, code: "lib.Save(1)",
		},
		{
			name: "a deleted file has no uses left",
			file: &impact.ChangedFile{OldPath: "a.go", Deleted: true},
			old:  "lib.Save(1)",
			site: 1, state: UseGone,
		},
		{
			name: "a file that no longer names the symbol has no uses left",
			file: changed(impact.ChangeBlock{OldLine: 9, NewLine: 9, Removed: []string{"x"}}),
			old:  "a\nb",
			new:  "a\nb",
			site: 1, state: UseGone,
		},
		{
			name: "an import line is open and says so",
			file: changed(impact.ChangeBlock{OldLine: 3, NewLine: 3, Removed: []string{"x"}, Added: []string{"y"}}),
			old:  "import { Save } from './lib'\n\nx",
			new:  "import { Save } from './lib'\n\ny",
			site: 1, state: UseOpen, line: 1, code: "import { Save } from './lib'", isImport: true,
		},
		{
			name: "an entry of a multi-line import is an import",
			file: changed(impact.ChangeBlock{OldLine: 6, NewLine: 6, Removed: []string{"x"}, Added: []string{"y"}}),
			old:  "import {\n  Other,\n  Save,\n} from './lib'\n\nx",
			new:  "import {\n  Other,\n  Save,\n} from './lib'\n\ny",
			site: 3, state: UseOpen, line: 3, code: "Save,", isImport: true,
		},
		{
			name: "an argument list isn't an import list",
			file: changed(impact.ChangeBlock{OldLine: 4, NewLine: 4, Removed: []string{"x"}, Added: []string{"y"}}),
			old:  "run(\n  Other,\n  Save,\n)x",
			new:  "run(\n  Other,\n  Save,\n)y",
			site: 3, state: UseOpen, line: 3, code: "Save,",
		},
		{
			name: "a line that only contains a longer word isn't the use",
			file: changed(impact.ChangeBlock{OldLine: 9, NewLine: 9, Removed: []string{"x"}}),
			old:  "Saved(1)\nlib.Save(2)",
			new:  "Saved(1)\nlib.Save(2)",
			site: 1, state: UseOpen, line: 1, code: "Saved(1)", unverified: true,
		},
		{
			name: "outside the diff, the use is open",
			new:  "func f() {\n\tlib.Save(1)\n}",
			site: 2, state: UseOpen, line: 2, code: "lib.Save(1)",
		},
		{
			name: "outside the diff, a moved use is flagged as unverified",
			new:  "func f() {\n\n\tlib.Save(1)\n}",
			site: 2, state: UseOpen, line: 2, unverified: true,
		},
		{
			name: "outside the diff, a file that dropped the name has no uses",
			new:  "func f() {\n}",
			site: 2, state: UseGone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var oldLines []string
			if tt.old != "" {
				oldLines = lines(tt.old)
			}
			got := ClassifyUse(tt.file, oldLines, lines(tt.new), UseSite{Line: tt.site, Name: name})
			if got.State != tt.state {
				t.Fatalf("state = %d, want %d (%+v)", got.State, tt.state, got)
			}
			if got.Line != tt.line {
				t.Errorf("line = %d, want %d", got.Line, tt.line)
			}
			if got.Code != tt.code {
				t.Errorf("code = %q, want %q", got.Code, tt.code)
			}
			if got.Unverified != tt.unverified {
				t.Errorf("unverified = %v, want %v", got.Unverified, tt.unverified)
			}
			if got.Import != tt.isImport {
				t.Errorf("import = %v, want %v", got.Import, tt.isImport)
			}
		})
	}
}

// The real parser's blocks, not hand-made ones, have to line up with the mapping.
func TestClassifyUse_ParsedDiff(t *testing.T) {
	parsed, err := NewGitDiffParser().Parse(`diff --git a/b/b.go b/b/b.go
--- a/b/b.go
+++ b/b/b.go
@@ -2,7 +2,7 @@ package b
 
 import "example.com/repro/lib"
 
-// Run calls Save.
+// Run calls Save, reworded.
 func Run() int { return lib.Save(1) }
 
 func Other() {}
`)
	if err != nil {
		t.Fatal(err)
	}
	old := lines("package b\n\nimport \"example.com/repro/lib\"\n\n// Run calls Save.\nfunc Run() int { return lib.Save(1) }\n\nfunc Other() {}\n")
	now := lines("package b\n\nimport \"example.com/repro/lib\"\n\n// Run calls Save, reworded.\nfunc Run() int { return lib.Save(1) }\n\nfunc Other() {}\n")
	got := ClassifyUse(&parsed.Files[0], old, now, UseSite{Line: 6, Column: 34, Name: "Save"})
	if got.State != UseOpen || got.Line != 6 {
		t.Errorf("got %+v, want the call on line 6 open", got)
	}
}

func TestIsCodeFile(t *testing.T) {
	for path, want := range map[string]bool{
		"main.go": true, "scripts/gen.py": true, "a.vue": true, "Makefile": true,
		"README.md": false, "docs/a.MD": false, ".ckb/config.json": false, "ci.yml": false,
	} {
		if got := isCodeFile(path); got != want {
			t.Errorf("isCodeFile(%q) = %v, want %v", path, got, want)
		}
	}
}
