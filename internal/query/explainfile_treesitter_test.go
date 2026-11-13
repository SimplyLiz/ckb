//go:build cgo

package query

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Without a SCIP index explainFile listed no symbols for any file; the
// tree-sitter parser still knows what a file defines.
func TestExplainFile_NoIndexListsParsedSymbols(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	src := "def price(x):\n    return x * 2\n\n\ndef _helper():\n    pass\n"
	if err := os.WriteFile(filepath.Join(engine.repoRoot, "app.py"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	resp, err := engine.ExplainFile(context.Background(), ExplainFileOptions{FilePath: "app.py"})
	if err != nil {
		t.Fatalf("ExplainFile: %v", err)
	}
	got := map[string]bool{}
	for _, s := range resp.Facts.Symbols {
		got[s.Name] = true
	}
	if !got["price"] || !got["_helper"] {
		t.Fatalf("symbols = %+v, want price and _helper", resp.Facts.Symbols)
	}
	exported := map[string]bool{}
	for _, e := range resp.Facts.Exports {
		exported[e] = true
	}
	if !exported["price"] || exported["_helper"] {
		t.Fatalf("exports = %v, want price only", resp.Facts.Exports)
	}
}
