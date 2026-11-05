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
		{"ts interface member", "ts", []string{"  run(a: string): void", "  stop(): void"}, "run(a: string): void", true},
		{"ts type alias", "ts", []string{"type A = { x: string }"}, "type A = { x: string }", true},
		{"ts string with slashes", "ts", []string{`function f(u = "http://x") {`, "}"}, `function f(u = "http://x")`, true},
		{"ts multi-line return", "ts", []string{"function f(a: A):", "  Promise<R> {"}, "function f(a: A): Promise<R>", true},

		{"rust where", "rust", []string{"pub fn f<T>(a: T) -> T", "where", "    T: Clone,", "{", "    a"}, "pub fn f<T>(a: T) -> T where T: Clone,", true},
		{"rust lifetime", "rust", []string{"fn f<'a>(a: &'a str) -> &'a str {", "  a"}, "fn f<'a>(a: &'a str) -> &'a str", true},
		{"rust trait method", "rust", []string{"    fn f(&self);", "    fn g(&self);"}, "fn f(&self)", true},
		{"rust const", "rust", []string{"pub const LIMIT: usize = 3;"}, "pub const LIMIT: usize", true},
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
