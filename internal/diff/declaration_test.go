package diff

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

func TestCompareDeclarations(t *testing.T) {
	const (
		same       = impact.DeclUnchanged
		compatible = impact.DeclCompatible
		changed    = impact.DeclChanged
	)
	tests := []struct {
		name, lang, symbol, before, after string
		want                              impact.DeclarationChange
	}{
		{"go added parameter", "go", "F", "func F(a int)", "func F(a int, b string)", changed},
		{"go removed parameter", "go", "F", "func F(a, b int)", "func F(a int)", changed},
		{"go parameter type", "go", "F", "func F(a int)", "func F(a int64)", changed},
		{"go grouped type change", "go", "F", "func F(a, b int)", "func F(a int, b string)", changed},
		{"go return type", "go", "F", "func F() int", "func F() (int, error)", changed},
		{"go parameter renamed", "go", "F", "func F(a int)", "func F(count int)", same},
		{"go grouped reflow", "go", "F", "func F(a, b int)", "func F(a int, b int)", same},
		{"go reformatted", "go", "F", "func F(\n a int,\n)", "func F(a int)", same},
		{"go receiver var renamed", "go", "M", "func (a *T) M(x int)", "func (b *T) M(x int)", same},
		{"go receiver type", "go", "M", "func (a *T) M()", "func (a T) M()", changed},
		{"go variadic appended", "go", "F", "func F(a int)", "func F(a int, opts ...Option)", compatible},
		{"go unnamed func type", "go", "F", "func F(func(int) int)", "func F(func(int) string)", changed},
		{"go field type", "go", "Name", "Name string", "Name []string", changed},

		{"ts added required", "ts", "f", "export function f(a: string)", "export function f(a: string, b: number)", changed},
		{"ts added optional", "ts", "f", "export function f(a: string)", "export function f(a: string, b?: number)", compatible},
		{"ts added default", "ts", "f", "function f(a: string)", "function f(a: string, b = 1)", compatible},
		{"ts added rest", "ts", "f", "function f(a: string)", "function f(a: string, ...b: number[])", compatible},
		{"ts became optional", "ts", "f", "function f(a: string)", "function f(a?: string)", compatible},
		{"ts became required", "ts", "f", "function f(a?: string)", "function f(a: string)", changed},
		{"ts renamed", "ts", "f", "function f(a: string)", "function f(b: string)", same},
		{"ts destructured renamed", "ts", "f", "function f({a}: P)", "function f({a, b}: P)", same},
		{"ts type changed", "ts", "f", "function f(a: string)", "function f(a: number)", changed},
		{"ts return changed", "ts", "f", "function f(): A", "function f(): B", changed},
		{"ts async added", "ts", "f", "function f(): A", "async function f(): A", changed},
		{"ts generic params", "ts", "f", "function f<T>(a: T)", "function f<T>(a: T, b?: T)", compatible},
		{"ts arrow added required", "ts", "f", "export const f = (a: string) =>", "export const f = (a: string, b: B) =>", changed},
		{"ts arrow renamed", "ts", "f", "const f = async (a: string) =>", "const f = async (b: string) =>", same},
		{"ts export added", "ts", "f", "function f(a: string)", "export function f(a: string)", compatible},
		{"ts export removed", "ts", "f", "export function f(a: string)", "function f(a: string)", changed},
		{"ts made private", "ts", "m", "m(a: string)", "private m(a: string)", changed},
		{"ts object param type", "ts", "f", "function f(a: {x: string})", "function f(a: {x: number})", changed},

		{"rust added parameter", "rust", "f", "pub fn f(a: i32)", "pub fn f(a: i32, b: &str)", changed},
		{"rust renamed", "rust", "f", "pub fn f(a: i32)", "pub fn f(b: i32)", same},
		{"rust mut binding", "rust", "f", "fn f(a: i32)", "fn f(mut a: i32)", same},
		{"rust self changed", "rust", "f", "fn f(&self)", "fn f(&mut self)", changed},
		{"rust return", "rust", "f", "fn f() -> i32", "fn f() -> Result<i32, E>", changed},
		{"rust path type", "rust", "f", "fn f(a: std::io::Error)", "fn f(a: anyhow::Error)", changed},
		{"rust pub added", "rust", "f", "fn f(a: i32)", "pub fn f(a: i32)", compatible},
		{"rust pub narrowed", "rust", "f", "pub fn f(a: i32)", "pub(crate) fn f(a: i32)", changed},
		{"rust generics", "rust", "f", "fn f<T: Fn(i32) -> i32>(a: T)", "fn f<T: Fn(i32) -> i32>(b: T)", same},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareDeclarations(tt.before, tt.after, tt.symbol, tt.lang); got != tt.want {
				t.Errorf("%q -> %q = %q, want %q", tt.before, tt.after, got, tt.want)
			}
		})
	}
}

func TestReadDeclaration(t *testing.T) {
	tests := []struct {
		name, lang string
		lines      []string
		want       string
		complete   bool
	}{
		{"go one line", "go", []string{"func F(a int) error {", "\treturn nil", "}"}, "func F(a int) error", true},
		{"go multi-line", "go", []string{"func F(", "\ta int, // why", "\tb string,", ") error {", "\treturn nil"}, "func F( a int, b string, ) error", true},
		{"go interface method", "go", []string{"\tRead(p []byte) (int, error)", "\tClose() error"}, "Read(p []byte) (int, error)", true},
		{"go field", "go", []string{"\tName string `json:\"name\"`", "\tAge int"}, "Name string `json:\"name\"`", true},
		{"go const value left out", "go", []string{"const Limit = 3"}, "const Limit", true},
		{"go type alias kept", "go", []string{"type A = B"}, "type A = B", true},
		{"go runs out of diff", "go", []string{"func F(", "\ta int,"}, "func F( a int,", false},

		{"ts function", "ts", []string{"export function f(a: string): Promise<R> {", "  return g(a)"}, "export function f(a: string): Promise<R>", true},
		{"ts object return type", "ts", []string{"function f(): { a: string } {", "  return x"}, "function f(): { a: string }", true},
		{"ts arrow body", "ts", []string{"export const f = (a: string) => a.trim()"}, "export const f = (a: string) =>", true},
		{"ts arrow block", "ts", []string{"const f = async (a: string): Promise<void> => {", "  await a"}, "const f = async (a: string): Promise<void> =>", true},
		{"ts plain const", "ts", []string{"export const limit: number = 3"}, "export const limit: number", true},
		{"ts const of a generic type", "ts", []string{"export const seen: Map<string, number> = new Map()"}, "export const seen: Map<string, number>", true},
		{"ts interface member", "ts", []string{"  run(a: string): void", "  stop(): void"}, "run(a: string): void", true},
		{"ts type alias", "ts", []string{"type A = { x: string }"}, "type A = { x: string }", true},
		{"ts string with slashes", "ts", []string{`function f(u = "http://x") {`, "}"}, `function f(u = "http://x")`, true},
		{"ts multi-line return", "ts", []string{"function f(a: A):", "  Promise<R> {"}, "function f(a: A): Promise<R>", true},

		{"rust where", "rust", []string{"pub fn f<T>(a: T) -> T", "where", "    T: Clone,", "{", "    a"}, "pub fn f<T>(a: T) -> T where T: Clone,", true},
		{"rust lifetime", "rust", []string{"fn f<'a>(a: &'a str) -> &'a str {", "  a"}, "fn f<'a>(a: &'a str) -> &'a str", true},
		{"rust trait method", "rust", []string{"    fn f(&self);", "    fn g(&self);"}, "fn f(&self)", true},
		{"rust const", "rust", []string{"pub const LIMIT: usize = 3;"}, "pub const LIMIT: usize", true},
		{"rust static of a generic type", "rust", []string{"static NUMBERED: LazyLock<Vec<(Kind, Regex)>> = LazyLock::new(|| {", "    vec![]"}, "static NUMBERED: LazyLock<Vec<(Kind, Regex)>>", true},
		{"rust default with >=", "rust", []string{"const OK: bool = A >= B;"}, "const OK: bool", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := make(map[int]string)
			for i, l := range tt.lines {
				lines[10+i] = l
			}
			got := readDeclaration(lines, 10, tt.lang)
			if spaceRe.ReplaceAllString(got.text, " ") != tt.want || got.complete != tt.complete {
				t.Errorf("got %q complete=%v, want %q complete=%v", got.text, got.complete, tt.want, tt.complete)
			}
		})
	}
}

// A hunk as the parser produces it, for a definition on line 3 of the old file.
// classifyDeclaration classifies from the hunks alone, as without a file reader.
func classifyDeclaration(file *impact.ChangedFile, def *SymbolDefInfo, name string, side indexSide, changeType impact.ChangeType, path string) (impact.DeclarationChange, string, string) {
	if file.IsNew {
		return "", "", ""
	}
	return classifyDeclarationIn(readFileText(file, nil), file.Deleted, def, name, side, changeType, path)
}

func changedFile(path string, oldStart int, oldText, newText []string, removed, added []int) *impact.ChangedFile {
	return &impact.ChangedFile{OldPath: path, NewPath: path, Hunks: []impact.ChangedHunk{{
		OldStart: oldStart, NewStart: oldStart, OldText: oldText, NewText: newText, Removed: removed, Added: added,
	}}}
}

func TestClassifyDeclaration(t *testing.T) {
	def := &SymbolDefInfo{StartLine: 3, EndLine: 5, DefLine: 3}

	t.Run("body only", func(t *testing.T) {
		file := changedFile("a.go", 2,
			[]string{"", "func F(a int) int {", "\treturn a", "}"},
			[]string{"", "func F(a int) int {", "\treturn a + 1", "}"},
			[]int{4}, []int{4})
		got, _, _ := classifyDeclaration(file, def, "F", indexMatchesOld, impact.ChangeModified, "a.go")
		if got != impact.DeclUnchanged {
			t.Errorf("got %q", got)
		}
	})

	t.Run("signature on the index's side", func(t *testing.T) {
		file := changedFile("a.go", 2,
			[]string{"", "func F(a int) int {", "\treturn a", "}"},
			[]string{"", "func F(a int, b int) int {", "\treturn a", "}"},
			[]int{3}, []int{3})
		got, before, after := classifyDeclaration(file, def, "F", indexMatchesOld, impact.ChangeModified, "a.go")
		if got != impact.DeclChanged || before != "func F(a int) int" || after != "func F(a int, b int) int" {
			t.Errorf("got %q %q -> %q", got, before, after)
		}
	})

	t.Run("signature with the index already past the change", func(t *testing.T) {
		file := changedFile("a.go", 2,
			[]string{"", "func F(a int) int {", "\treturn a", "}"},
			[]string{"", "func F(a int, b int) int {", "\treturn a", "}"},
			[]int{3}, []int{3})
		got, before, _ := classifyDeclaration(file, def, "F", indexMatchesNew, impact.ChangeModified, "a.go")
		if got != impact.DeclChanged || before != "func F(a int) int" {
			t.Errorf("got %q, before %q", got, before)
		}
	})

	t.Run("a later line of a multi-line signature", func(t *testing.T) {
		file := changedFile("a.go", 2,
			[]string{"", "func F(", "\ta int,", ") int {", "\treturn a"},
			[]string{"", "func F(", "\ta string,", ") int {", "\treturn a"},
			[]int{4}, []int{4})
		got, _, _ := classifyDeclaration(file, def, "F", indexMatchesOld, impact.ChangeModified, "a.go")
		if got != impact.DeclChanged {
			t.Errorf("got %q", got)
		}
	})

	t.Run("renamed away", func(t *testing.T) {
		file := changedFile("a.go", 2,
			[]string{"", "func F(a int) int {", "\treturn a", "}"},
			[]string{"", "func G(a int) int {", "\treturn a", "}"},
			[]int{3}, []int{3})
		got, _, _ := classifyDeclaration(file, def, "F", indexMatchesOld, impact.ChangeModified, "a.go")
		if got != impact.DeclRemoved {
			t.Errorf("got %q", got)
		}
	})

	t.Run("a call in the replacement is not a declaration", func(t *testing.T) {
		file := changedFile("a.go", 2,
			[]string{"", "func F(a int) int {", "\treturn a", "}"},
			[]string{"", "func G(a int) int {", "\treturn F(a)", "}"},
			[]int{3, 4}, []int{3, 4})
		got, _, _ := classifyDeclaration(file, def, "F", indexMatchesOld, impact.ChangeModified, "a.go")
		if got != impact.DeclRemoved {
			t.Errorf("got %q", got)
		}
	})

	t.Run("same method name on another type", func(t *testing.T) {
		file := changedFile("a.go", 2,
			[]string{"", "func (t *T) M() {", "}", ""},
			[]string{"", "func (u *U) M() {", "}", ""},
			[]int{3}, []int{3})
		got, _, _ := classifyDeclaration(file, def, "M", indexMatchesOld, impact.ChangeModified, "a.go")
		if got != impact.DeclRemoved {
			t.Errorf("got %q", got)
		}
	})

	t.Run("no hunk reaches the declaration", func(t *testing.T) {
		far := &SymbolDefInfo{StartLine: 1, EndLine: 20, DefLine: 1}
		file := changedFile("a.go", 10, []string{"\tx := 1"}, []string{"\tx := 2"}, []int{10}, []int{10})
		got, _, _ := classifyDeclaration(file, far, "F", indexMatchesOld, impact.ChangeModified, "a.go")
		if got != impact.DeclUnchanged {
			t.Errorf("got %q", got)
		}
	})

	t.Run("a language it can't read gets no answer", func(t *testing.T) {
		file := changedFile("a.py", 2, []string{"def F(a):"}, []string{"def F(a, b):"}, []int{3}, []int{3})
		got, _, _ := classifyDeclaration(file, def, "F", indexMatchesOld, impact.ChangeModified, "a.py")
		if got != "" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("a TypeScript arrow turned into a function with a new parameter", func(t *testing.T) {
		file := changedFile("a.ts", 2,
			[]string{"", "export const f = (a: string) => {", "  return a", "}"},
			[]string{"", "export function f(a: string, b: number) {", "  return a", "}"},
			[]int{3}, []int{3})
		got, _, _ := classifyDeclaration(file, def, "f", indexMatchesOld, impact.ChangeModified, "a.ts")
		if got != impact.DeclChanged {
			t.Errorf("got %q", got)
		}
	})

	t.Run("Rust method body", func(t *testing.T) {
		file := changedFile("a.rs", 2,
			[]string{"", "    pub fn run(&self, a: i32) -> i32 {", "        a", "    }"},
			[]string{"", "    pub fn run(&self, a: i32) -> i32 {", "        a + 1", "    }"},
			[]int{4}, []int{4})
		got, _, _ := classifyDeclaration(file, def, "run", indexMatchesOld, impact.ChangeModified, "a.rs")
		if got != impact.DeclUnchanged {
			t.Errorf("got %q", got)
		}
	})
}

// A Go constant moved into a const block is the same declaration, not a removed
// one: inside the block the line has no keyword.
func TestClassifyDeclaration_GoGroupedDeclaration(t *testing.T) {
	def := &SymbolDefInfo{StartLine: 3, EndLine: 3, DefLine: 3}
	old := []string{"", "const X = 20", ""}
	cases := []struct {
		name string
		new  []string
		want impact.DeclarationChange
	}{
		{"moved into a block", []string{"", "const (", "\tX = 20", "\tY = 50", ")", ""}, impact.DeclUnchanged},
		{"moved and typed", []string{"", "const (", "\tX string = \"a\"", ")", ""}, impact.DeclChanged},
		{"block without it", []string{"", "const (", "\tY = 50", ")", ""}, impact.DeclRemoved},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var added []int
			for i := 1; i < len(c.new)-1; i++ {
				added = append(added, 2+i)
			}
			file := changedFile("a.go", 2, old, c.new, []int{3}, added)
			got, before, after := classifyDeclaration(file, def, "X", indexMatchesOld, impact.ChangeModified, "a.go")
			if got != c.want {
				t.Errorf("got %q (%q -> %q), want %q", got, before, after, c.want)
			}
		})
	}

	// Out of a block, the other way round.
	def = &SymbolDefInfo{StartLine: 4, EndLine: 4, DefLine: 4}
	file := changedFile("a.go", 2,
		[]string{"", "const (", "\tX = 20", ")", ""},
		[]string{"", "const X = 20", ""},
		[]int{3, 4, 5}, []int{3})
	if got, before, after := classifyDeclaration(file, def, "X", indexMatchesOld, impact.ChangeModified, "a.go"); got != impact.DeclUnchanged {
		t.Errorf("out of a block: got %q (%q -> %q)", got, before, after)
	}
}

// A constant moved out of a long `const (` block whose opening line the hunk
// doesn't reach: the whole file says it is in the block.
func TestClassifyDeclaration_GoGroupHeaderOutsideHunk(t *testing.T) {
	oldFile := []string{"package a", "", "const ("}
	for _, n := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P"} {
		oldFile = append(oldFile, "\t"+n+" = 1")
	}
	// line 20: X; 21: ")"
	oldFile = append(oldFile, "\tX = 20", ")", "")
	newFile := append(append([]string{}, oldFile[:19]...), ")", "", "const X = 20", "")
	blockOnly := append(append([]string{}, oldFile[:19]...), ")", "")

	// Hunk from line 17: three lines of context, X removed, `const X` added after.
	moved := changedFile("a.go", 17,
		[]string{"\tN = 1", "\tO = 1", "\tP = 1", "\tX = 20", ")", ""},
		[]string{"\tN = 1", "\tO = 1", "\tP = 1", ")", "", "const X = 20", ""},
		[]int{20}, []int{22, 23})
	gone := changedFile("a.go", 17,
		[]string{"\tN = 1", "\tO = 1", "\tP = 1", "\tX = 20", ")", ""},
		[]string{"\tN = 1", "\tO = 1", "\tP = 1", ")", ""},
		[]int{20}, nil)
	def := &SymbolDefInfo{StartLine: 20, EndLine: 20, DefLine: 20}

	cases := []struct {
		name  string
		file  *impact.ChangedFile
		whole *fileSides
		want  impact.DeclarationChange
	}{
		{"moved out, whole file read", moved, &fileSides{old: oldFile, new: newFile}, impact.DeclUnchanged},
		{"removed from the block", gone, &fileSides{old: oldFile, new: blockOnly}, impact.DeclRemoved},
		// Text that doesn't match the hunks isn't the file the diff was made from.
		{"whole file from another revision", moved, &fileSides{old: blockOnly, new: newFile}, impact.DeclRemoved},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, before, after := classifyDeclarationIn(readFileText(c.file, c.whole), false, def, "X", indexMatchesOld, impact.ChangeModified, "a.go")
			if got != c.want {
				t.Errorf("got %q (%q -> %q), want %q", got, before, after, c.want)
			}
		})
	}
}

// Go's other declaration forms, each across a diff whose hunk shows it whole.
func TestClassifyDeclaration_GoForms(t *testing.T) {
	cases := []struct {
		name, symbol string
		old, new     []string
		removed      []int
		added        []int
		defLine      int
		want         impact.DeclarationChange
	}{
		{"iota continuation after the first changed", "B",
			[]string{"const (", "\tA = iota", "\tB", ")"}, []string{"const (", "\tA = iota + 1", "\tB", ")"},
			[]int{3}, []int{3}, 4, impact.DeclUnchanged},
		{"two names, one value changed", "b",
			[]string{"var a, b = 1, 2"}, []string{"var a, b = 1, 3"}, []int{2}, []int{2}, 2, impact.DeclUnchanged},
		{"two names into a var block", "b",
			[]string{"var a, b = 1, 2", ""}, []string{"var (", "\ta, b = 1, 2", "\tc = 3", ")", ""}, []int{2}, []int{2, 3, 4, 5}, 2, impact.DeclUnchanged},
		{"type into a type block", "T",
			[]string{"type T struct {", "\tX int", "}"}, []string{"type (", "\tT struct {", "\t\tX int", "\t}", ")"},
			[]int{2, 3, 4}, []int{2, 3, 4, 5, 6}, 2, impact.DeclUnchanged},
		{"defined type to alias", "T",
			[]string{"type T int"}, []string{"type T = int"}, []int{2}, []int{2}, 2, impact.DeclChanged},
		{"constant typed", "X",
			[]string{"const X = 1"}, []string{"const X int64 = 1"}, []int{2}, []int{2}, 2, impact.DeclChanged},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old := append(append([]string{""}, c.old...), "")
			new := append(append([]string{""}, c.new...), "")
			file := changedFile("a.go", 1, old, new, c.removed, c.added)
			def := &SymbolDefInfo{StartLine: c.defLine, EndLine: c.defLine, DefLine: c.defLine}
			got, before, after := classifyDeclaration(file, def, c.symbol, indexMatchesOld, impact.ChangeModified, "a.go")
			if got != c.want {
				t.Errorf("got %q (%q -> %q), want %q", got, before, after, c.want)
			}
		})
	}
}

// A parameter with the symbol's name, in another declaration's signature, doesn't
// declare it: the name has to follow its keyword.
func TestFindDeclaration_NameAsParameter(t *testing.T) {
	cases := []struct {
		lang, name, like string
		lines            []string
		want             int
	}{
		{"go", "file", "func (r *T) file(p string) *F",
			[]string{"func (r *T) line(id string) (text string, file *F, ok bool) {", "func (r *T) file(p string) *F {"}, 2},
		{"go", "file", "func file(p string) *F",
			[]string{"func open(file string) error {"}, 0},
		{"go", "b", "var a, b = 1, 2", []string{"var a, b = 1, 3"}, 1},
		{"ts", "file", "function file(p: string)", []string{"function open(file: string) {", "export function file(p: string) {"}, 2},
		{"rust", "COUNT", "static mut COUNT: u32 = 0", []string{"static mut COUNT: u64 = 0;"}, 1},
		{"rust", "file", "pub fn file(p: &str)", []string{"fn open(file: &str) {", "pub(crate) fn file(p: &str) {"}, 2},
	}
	for _, c := range cases {
		lines := make(map[int]string)
		changed := make(map[int]bool)
		for i, l := range c.lines {
			lines[i+1] = l
			changed[i+1] = true
		}
		if got := findDeclaration(lines, changed, c.like, c.name, c.lang); got != c.want {
			t.Errorf("%s %q: got line %d, want %d", c.lang, c.like, got, c.want)
		}
	}
}

// A TypeScript function that loses `export` but is named in the file's export list
// is still exported. A list re-exporting from another module doesn't count.
func TestClassifyDeclaration_TSExportList(t *testing.T) {
	old := []string{"", "export function f(a: string) {", "  return a;", "}", ""}
	def := &SymbolDefInfo{StartLine: 3, EndLine: 5, DefLine: 3}
	cases := []struct {
		name string
		tail []string
		want impact.DeclarationChange
	}{
		{"listed", []string{"export { f, g };"}, impact.DeclUnchanged},
		{"listed over lines", []string{"export {", "  g,", "  f,", "};"}, impact.DeclUnchanged},
		{"listed under another name", []string{"export { f as h };"}, impact.DeclChanged},
		{"re-exported from elsewhere", []string{"export { f } from './other';"}, impact.DeclChanged},
		{"not listed", []string{"export { g };"}, impact.DeclChanged},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newText := append([]string{"", "function f(a: string) {", "  return a;", "}", ""}, c.tail...)
			file := changedFile("a.ts", 2, old, newText[:5], []int{3}, []int{3})
			whole := &fileSides{
				old: []string{"import x from 'x';", "", "export function f(a: string) {", "  return a;", "}", ""},
				new: append([]string{"import x from 'x';", "", "function f(a: string) {", "  return a;", "}", ""}, c.tail...),
			}
			got, before, after := classifyDeclarationIn(readFileText(file, whole), false, def, "f", indexMatchesOld, impact.ChangeModified, "a.ts")
			if got != c.want {
				t.Errorf("got %q (%q -> %q), want %q", got, before, after, c.want)
			}
		})
	}
}
