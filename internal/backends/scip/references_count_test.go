package scip

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
)

// buildRefCountIndex builds an in-memory index with one symbol that has one
// definition plus nReads read occurrences, all in a single document. When
// withRefIndex is true the inverted RefIndex is populated (the fast path);
// otherwise FindReferences/CountReferences fall back to the document scan.
func buildRefCountIndex(nReads int, withRefIndex bool) (*SCIPIndex, string) {
	const sym = "scip-go gomod example.com/m `pkg`/Target()."

	doc := &Document{RelativePath: "pkg/target.go"}
	doc.Occurrences = append(doc.Occurrences, &Occurrence{
		Range:       []int32{1, 5, 11},
		Symbol:      sym,
		SymbolRoles: SymbolRoleDefinition,
	})
	for i := 0; i < nReads; i++ {
		doc.Occurrences = append(doc.Occurrences, &Occurrence{
			Range:  []int32{int32(10 + i), 2, 8},
			Symbol: sym,
		})
	}

	idx := &SCIPIndex{
		Documents:       []*Document{doc},
		DocumentsByPath: map[string]*Document{doc.RelativePath: doc},
	}
	if withRefIndex {
		idx.RefIndex = map[string][]*OccurrenceRef{}
		for _, occ := range doc.Occurrences {
			idx.RefIndex[occ.Symbol] = append(idx.RefIndex[occ.Symbol], &OccurrenceRef{Doc: doc, Occ: occ})
		}
	}
	return idx, sym
}

// TestFindReferences_TotalIsNotCappedByMaxResults mirrors what the query engine
// does: it asks for Limit*2 references and must still see the real total.
func TestFindReferences_TotalIsNotCappedByMaxResults(t *testing.T) {
	t.Parallel()

	// 17 occurrences in total: 1 definition + 16 reads (the issue's repro shape).
	const nReads = 16
	const total = nReads + 1

	tests := []struct {
		name               string
		limit              int
		includeDeclaration bool
		wantTotal          int
		wantReturned       int
	}{
		{"limit below total", 5, true, total, 10},
		{"limit equal to total", 17, true, total, total},
		{"limit above total", 100, true, total, total},
		{"limit below total, declaration excluded", 5, false, nReads, 10},
		{"limit above total, declaration excluded", 100, false, nReads, nReads},
	}

	for _, withRefIndex := range []bool{true, false} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("refIndex=%v/%s", withRefIndex, tt.name), func(t *testing.T) {
				t.Parallel()

				idx, sym := buildRefCountIndex(nReads, withRefIndex)
				adapter := &SCIPAdapter{
					index:        idx,
					logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
					queryTimeout: time.Second,
				}

				res, err := adapter.FindReferences(t.Context(), sym, backends.RefOptions{
					MaxResults:         tt.limit * 2,
					IncludeDeclaration: tt.includeDeclaration,
				})
				if err != nil {
					t.Fatalf("FindReferences: %v", err)
				}
				if got := len(res.References); got != tt.wantReturned {
					t.Errorf("returned refs = %d, want %d", got, tt.wantReturned)
				}
				if res.TotalReferences != tt.wantTotal {
					t.Errorf("TotalReferences = %d, want %d", res.TotalReferences, tt.wantTotal)
				}
			})
		}
	}
}

func TestCountReferences_UnknownSymbolIsZero(t *testing.T) {
	t.Parallel()
	idx, _ := buildRefCountIndex(3, true)
	if got := idx.CountReferences("scip-go gomod nope `x`/Missing().", ReferenceOptions{IncludeDefinition: true}); got != 0 {
		t.Errorf("CountReferences(unknown) = %d, want 0", got)
	}
}

func TestFindReferences_ContextIsTheSourceLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const sym = "scip-go gomod example.com/m v1 `example.com/m/lib`/Save()."
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\n\n\tlib.Save(1)  \nlib.Saved(2)\n")
	long := strings.Repeat("é", 150) + " Save"
	write("long.go", long+"\n")

	occ := func(line int32) *Occurrence { return &Occurrence{Symbol: sym, Range: []int32{line, 5, 9}} }
	doc := func(path string, occs ...*Occurrence) *Document {
		return &Document{RelativePath: path, Occurrences: occs}
	}
	idx := &SCIPIndex{Documents: []*Document{
		doc("a.go", occ(2), occ(3), occ(99)), // The source line, a line lacking the name, past the end
		doc("gone.go", occ(0)),               // No such file
		doc("../outside.go", occ(0)),         // Not in the repo
		doc("long.go", occ(0)),
	}}
	adapter := &SCIPAdapter{
		index:        idx,
		repoRoot:     root,
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		queryTimeout: time.Second,
	}

	res, err := adapter.FindReferences(t.Context(), sym, backends.RefOptions{IncludeTests: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lib.Save(1)", "", "", "", ""}
	if len(res.References) != len(want)+1 {
		t.Fatalf("got %d references", len(res.References))
	}
	for i, context := range want {
		if got := res.References[i].Context; got != context {
			t.Errorf("reference %d at %+v: context %q, want %q", i, res.References[i].Location, got, context)
		}
	}
	got := res.References[len(want)].Context
	if !strings.HasSuffix(got, "…") || !utf8.ValidString(got) || len(got) > maxContextLength+len("…") {
		t.Errorf("long line: %q, want it cut on a character boundary", got)
	}
}
