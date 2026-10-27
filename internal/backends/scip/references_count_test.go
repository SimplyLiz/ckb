package scip

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

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
