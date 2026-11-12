package diff

import "testing"

// What the readers get wrong when each lexes Rust on its own: a char, a raw string or a
// comment taken for code. All of them read through rustCode now.

func TestRustCode_Kinds(t *testing.T) {
	code, kinds := newRustCode().kinds(`f('{', r#"a}"#, "x") // y`)
	if code != `f(' ', r#"  "#, " ")     ` {
		t.Errorf("code = %q", code)
	}
	if string(kinds[3]) != string(rustKindString) || string(kinds[len(kinds)-1]) != string(rustKindComment) || kinds[0] != rustKindCode {
		t.Errorf("kinds = %q", kinds)
	}
}

func TestReadDeclaration_RustLexing(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		// A `{` in a char of a const generic's default isn't the body's.
		{"char brace", []string{"pub fn f<const C: char = '{'>() -> u8 {", "    1"}, "pub fn f<const C: char = '{'>() -> u8"},
		// A string's text is kept: `extern "C"` and `extern "system"` differ.
		{"abi kept", []string{`pub extern "C" fn g(x: u8) {`}, `pub extern "C" fn g(x: u8)`},
		// A raw string with a quote in it doesn't open a string past its end.
		{"raw string", []string{`#[doc = r#"say "hi"{"#] pub fn h(a: u8) -> u8 {`}, `#[doc = r#"say "hi"{"#] pub fn h(a: u8) -> u8`},
	}
	for _, tc := range cases {
		lines := map[int]string{}
		for i, l := range tc.lines {
			lines[i+1] = l
		}
		got := readDeclaration(lines, 1, "rust")
		if spaceRe.ReplaceAllString(got.text, " ") != tc.want || !got.complete {
			t.Errorf("%s: got %q complete=%v, want %q", tc.name, got.text, got.complete, tc.want)
		}
	}
}

func TestRustMatchAt_KeywordInComment(t *testing.T) {
	// `match` in a comment above the arm is no match; the real one is found.
	src := lines("fn f(e: E) -> u8 {\n    match e {\n        // match x {\n        E::A => 1,\n    }\n}\n")
	m, ok := RustMatchAt(src, 4, 0, "E")
	if !ok || m.Line != 2 {
		t.Errorf("got %+v %v, want the match on line 2", m, ok)
	}
	// A raw string's `}` in an arm's body doesn't end the match.
	src = lines("fn f(e: E) -> &str {\n    match e {\n        E::A => r#\"}\"#,\n        E::B => \"b\",\n    }\n}\n")
	if m, ok := RustMatchAt(src, 4, 0, "E"); !ok || m.Line != 2 {
		t.Errorf("raw string: got %+v %v", m, ok)
	}
}

func TestArmPatterns_CommentHasNoAlternative(t *testing.T) {
	alts, _, guard := armPatterns("E::A // | E::B, old\n    | E::C")
	if len(alts) != 2 || guard != -1 {
		t.Errorf("alts = %q guard %d, want E::A and E::C", alts, guard)
	}
	// `if` in a string is no guard.
	if _, _, guard := armPatterns(`E::Named("if")`); guard != -1 {
		t.Errorf("guard at %d in a string", guard)
	}
}

func TestRustLetAt_StringInPattern(t *testing.T) {
	// A `:` in a string literal of the pattern doesn't end it.
	src := lines("fn f(t: (&str, E)) {\n    let (\"a:b\", E::A) = t;\n}\n")
	if line, ok := RustLetAt(src, 2, 0, "E"); !ok || line != 2 {
		t.Errorf("got %d %v, want the let on line 2", line, ok)
	}
}
