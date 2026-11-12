package diff

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestRustEnumVariants(t *testing.T) {
	src := lines(`#[derive(Debug, thiserror::Error)]
pub enum DraftError {
    #[error("Entwurf nicht gefunden")]
    NotFound,
    /// Ein Kommentar, mit Komma.
    Conflict(String),
    Bounds { min: u32, max: u32 },
    #[error(transparent)]
    Db(#[from] sqlx::Error),
}`)
	got, state := RustEnumVariants(src, "DraftError")
	want := []string{"NotFound", "Conflict", "Bounds", "Db"}
	if state != BodyRead || !slices.Equal(got, want) {
		t.Fatalf("got %v %v, want %v", got, state, want)
	}
	if _, state := RustEnumVariants(src, "Other"); state != BodyNone {
		t.Fatal("found an enum that isn't there")
	}
	// The `(` of `pub(crate)` is no tuple; a `]` or `//` in an attribute's string is
	// no part of the body (temporal's sdk-core spells its error codes `[TMPRL1100]`).
	src = lines(`pub(crate) enum WFMachinesError {
    #[error("[TMPRL1100] Nondeterminism error: {0}")]
    Nondeterminism(String),
    #[error("see https://example.com/{x}, or not")]
    Moved { x: u8 },
    /* Fatal, */ Fatal(String),
}`)
	got, state = RustEnumVariants(src, "WFMachinesError")
	if want := []string{"Nondeterminism", "Moved", "Fatal"}; state != BodyRead || !slices.Equal(got, want) {
		t.Fatalf("got %v %v, want %v", got, state, want)
	}
}

func TestRustMatchAt(t *testing.T) {
	src := lines(`impl From<DraftError> for ApiError {
    fn from(e: DraftError) -> Self {
        match e {
            DraftError::NotFound => Self(StatusCode::NOT_FOUND, "x".into()),
            DraftError::Conflict(m) | DraftError::Invalid(m) => {
                let s = format!("{m}");
                Self(StatusCode::CONFLICT, s)
            }
            crate::drafts::DraftError::Db(d) => d.into(),
        }
    }
}

fn nested(x: Option<DraftError>) {
    match x {
        Some(DraftError::NotFound) => {}
        _ => {}
    }
}

fn caught(e: DraftError) -> u16 {
    match &e {
        DraftError::NotFound => 404,
        other if other.is_db() => 500,
        other => 400,
    }
}

fn body(e: DraftError) {
    match code {
        1 => DraftError::NotFound,
        _ => DraftError::Db(x),
    };
}`)
	cases := []struct {
		name     string
		line     int
		ok       bool
		matchAt  int
		catchAll bool
	}{
		{"first arm", 4, true, 3, false},
		{"after a block arm", 9, true, 3, false},
		{"second alternative", 5, true, 3, false},
		{"function signature", 2, false, 0, false},
		{"nested in Some", 16, true, 15, true},
		{"binding catches all", 23, true, 22, true},
		{"in an arm's body", 31, false, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			column := 0
			if tc.name == "second alternative" {
				column = 39 // The second DraftError on the line
			}
			m, ok := RustMatchAt(src, tc.line, column, "DraftError")
			if ok != tc.ok || ok && (m.Line != tc.matchAt || m.CatchAll != tc.catchAll) {
				t.Fatalf("got %+v %v, want line %d catchAll %v ok %v", m, ok, tc.matchAt, tc.catchAll, tc.ok)
			}
		})
	}

	m, _ := RustMatchAt(src, 4, 0, "DraftError")
	if !m.Names("Invalid") || !m.Names("Db") || m.Names("Unbelegt") || m.Names("StatusCode") {
		t.Fatalf("Names reads the wrong text: %+v", m)
	}
}

func TestRustMatchAt_Nested(t *testing.T) {
	cases := []struct {
		name            string
		src             string
		ok              bool
		nested, covered bool
	}{
		{"in Some, None beside", `match x {
    Some(E::A) => 1,
    None => 0,
}`, true, true, false},
		{"Some(_) takes the rest", `match x {
    Some(E::A) => 1,
    Some(_) => 2,
    None => 0,
}`, true, true, true},
		{"a binding takes the rest", `match x {
    Some(E::A) => 1,
    Some(other) => other.code(),
    None => 0,
}`, true, true, true},
		{"an arm with a guard takes nothing", `match x {
    Some(E::A) => 1,
    Some(y) if y.big() => 2,
    None => 0,
}`, true, true, false},
		{"another constructor takes nothing", `match r {
    Ok(E::A) => 1,
    Err(_) => 0,
}`, true, true, false},
		{"a tuple, the other place wild", `match (e, n) {
    (E::A, 1) => 1,
    (_, 2) => 2,
    (E::B, _) => 3,
}`, true, true, true},
		{"a tuple, its place never wild", `match (e, n) {
    (E::A, _) => 1,
    (E::B, _) => 2,
}`, true, true, false},
		{"a struct field, the field left to ..", `match f {
    Foo { kind: E::A, .. } => 1,
    Foo { n: 0, .. } => 2,
}`, true, true, true},
		{"a struct field bound by shorthand", `match f {
    Foo { kind: E::A, n } => n,
    Foo { kind, n: 0 } => 0,
}`, true, true, true},
		{"a struct field, never left open", `match f {
    Foo { kind: E::A, n } => n,
    Foo { kind: E::B, .. } => 0,
}`, true, true, false},
		{"Self(_) in an impl takes the rest", `match self {
    Foo(E::A) => 1,
    Self(_) => 0,
}`, true, true, true},
		{"alternatives inside Some", `match x {
    Some(E::B | E::A) => 1,
    None => 0,
}`, true, true, false},
		{"a reference is the arm itself", `match &e {
    &E::A => 1,
    &E::B => 2,
}`, true, false, false},
		{"in a guard", `match x {
    Some(y) if y == E::A => 1,
    _ => 0,
}`, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := lines(tc.src)
			m, ok := RustMatchAt(src, 2, 0, "A")
			if ok != tc.ok || ok && (m.Line != 1 || m.Nested != tc.nested || m.Covered != tc.covered || m.CatchAll) {
				t.Fatalf("got %+v %v, want nested %v covered %v ok %v", m, ok, tc.nested, tc.covered, tc.ok)
			}
		})
	}
}

// A clap subcommand enum documents every field and runs to hundreds of lines.
func TestRustEnumVariants_LongBody(t *testing.T) {
	var b strings.Builder
	b.WriteString("#[derive(Subcommand)]\nenum Commands {\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "    /// Mode %d\n    Mode%d {\n", i, i)
		for f := 0; f < 4; f++ {
			fmt.Fprintf(&b, "        /// Field %d\n        #[arg(long)]\n        field%d: Option<String>,\n", f, f)
		}
		b.WriteString("    },\n")
	}
	b.WriteString("}\n")
	src := lines(b.String())
	got, state := RustEnumVariants(src, "Commands")
	if state != BodyRead || len(got) != 60 || got[59] != "Mode59" || len(src) < 800 {
		t.Fatalf("%d lines: got %d variants, %v", len(src), len(got), state)
	}
}
