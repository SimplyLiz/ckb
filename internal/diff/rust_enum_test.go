package diff

import (
	"slices"
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
	got, ok := RustEnumVariants(src, "DraftError")
	want := []string{"NotFound", "Conflict", "Bounds", "Db"}
	if !ok || !slices.Equal(got, want) {
		t.Fatalf("got %v %v, want %v", got, ok, want)
	}
	if _, ok := RustEnumVariants(src, "Other"); ok {
		t.Fatal("found an enum that isn't there")
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
		{"nested in Some", 16, false, 0, false},
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
