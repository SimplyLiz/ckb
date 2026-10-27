//go:build !cgo

// Package symbols provides tree-sitter based symbol extraction for code intelligence fallback.
// This stub is used when CGO is not available.
package symbols

import (
	"context"

	"github.com/SimplyLiz/CodeMCP/internal/complexity"
)

// Symbol represents an extracted symbol from source code.
type Symbol struct {
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	Path       string  `json:"path"`
	Line       int     `json:"line"`
	EndLine    int     `json:"endLine"`
	Container  string  `json:"container"`
	Signature  string  `json:"signature"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

// Extractor extracts symbols from source files using tree-sitter.
// This is a stub implementation when CGO is not available.
type Extractor struct{}

// NewExtractor creates a new symbol extractor.
// Returns nil when CGO is not available.
func NewExtractor() *Extractor {
	return nil
}

// ExtractFile extracts all symbols from a single file.
// Returns empty when CGO is not available.
func (e *Extractor) ExtractFile(ctx context.Context, path string) ([]Symbol, error) {
	return nil, nil
}

// ExtractSource extracts symbols from source bytes.
// Returns empty when CGO is not available.
func (e *Extractor) ExtractSource(ctx context.Context, path string, source []byte, lang complexity.Language) ([]Symbol, error) {
	return nil, nil
}

// ExtractDirectory walks a directory and extracts all symbols.
// Returns empty when CGO is not available.
func (e *Extractor) ExtractDirectory(ctx context.Context, root string, filter func(string) bool) ([]Symbol, error) {
	return nil, nil
}

// ExtractDirectoryWithOptions returns empty when CGO is not available.
func (e *Extractor) ExtractDirectoryWithOptions(ctx context.Context, root string, opts DirOptions) ([]Symbol, error) {
	return nil, nil
}

// IsAvailable returns whether symbol extraction is available.
func IsAvailable() bool {
	return false
}
