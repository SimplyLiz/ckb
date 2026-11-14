//go:build cartographer

// Package cartographer provides CGo bindings to the Rust cartographer library.
// It enables CKB to perform fast architectural analysis, skeleton extraction,
// and layer enforcement without IPC or subprocess overhead.
//
// All functions are thread-safe and return JSON that is parsed into Go structs.
//
// Build with: go build -tags cartographer ./...
package cartographer

/*
#cgo darwin CFLAGS: -I${SRCDIR}/../../third_party/cartographer/include
#cgo darwin LDFLAGS: -L${SRCDIR}/../../third_party/cartographer/target/release -lcode_cartographer -lm -ldl -framework Security -framework CoreFoundation

#cgo linux CFLAGS: -I${SRCDIR}/../../third_party/cartographer/include
#cgo linux LDFLAGS: -L${SRCDIR}/../../third_party/cartographer/target/release -lcode_cartographer -lm -ldl -lpthread

#cgo windows CFLAGS: -I${SRCDIR}/../../third_party/cartographer/include
#cgo windows LDFLAGS: -L${SRCDIR}/../../third_party/cartographer/target/release -lcode_cartographer -lm

#include <stdlib.h>
#include "codecartographer.h"
*/
import "C"
import (
	"encoding/json"
	"unsafe"
)

// ffiResponse is the JSON envelope returned by all cartographer FFI functions.
type ffiResponse struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Available reports whether the cartographer library is linked into this binary.
func Available() bool { return true }

func callFFI(fn func() *C.char) (*ffiResponse, error) {
	cstr := fn()
	if cstr == nil {
		return nil, &CartographerError{"null response from FFI"}
	}
	defer C.codecartographer_free_string(cstr)

	goStr := C.GoString(cstr)
	var resp ffiResponse
	if err := json.Unmarshal([]byte(goStr), &resp); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	if !resp.OK {
		return nil, &CartographerError{resp.Error}
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Version returns the cartographer library version string (e.g. "1.1.0").
func Version() (string, error) {
	cstr := C.codecartographer_version()
	if cstr == nil {
		return "", &CartographerError{"null response from version"}
	}
	defer C.codecartographer_free_string(cstr)
	return C.GoString(cstr), nil
}

// MapProject scans a project directory and returns the full dependency graph.
func MapProject(path string) (*ProjectGraph, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_map_project(cPath)
	})
	if err != nil {
		return nil, err
	}

	var graph ProjectGraph
	if err := json.Unmarshal(resp.Data, &graph); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &graph, nil
}

// Health returns the architectural health score for a project.
func Health(path string) (*HealthReport, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_health(cPath)
	})
	if err != nil {
		return nil, err
	}

	var report HealthReport
	if err := json.Unmarshal(resp.Data, &report); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &report, nil
}

// CheckLayers validates a project against a layers.toml config.
// If layersPath is empty, uses default (empty) config — returns no violations.
func CheckLayers(path, layersPath string) ([]LayerViolation, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var cLayers *C.char
	if layersPath != "" {
		cLayers = C.CString(layersPath)
		defer C.free(unsafe.Pointer(cLayers))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_check_layers(cPath, cLayers)
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Violations     []LayerViolation `json:"violations"`
		ViolationCount int              `json:"violationCount"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return result.Violations, nil
}

// SimulateChange predicts the architectural impact of modifying a module.
func SimulateChange(path, moduleID, newSignature, removeSignature string) (*ImpactAnalysis, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cModule := C.CString(moduleID)
	defer C.free(unsafe.Pointer(cModule))

	var cNewSig, cRemSig *C.char
	if newSignature != "" {
		cNewSig = C.CString(newSignature)
		defer C.free(unsafe.Pointer(cNewSig))
	}
	if removeSignature != "" {
		cRemSig = C.CString(removeSignature)
		defer C.free(unsafe.Pointer(cRemSig))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_simulate_change(cPath, cModule, cNewSig, cRemSig)
	})
	if err != nil {
		return nil, err
	}

	var analysis ImpactAnalysis
	if err := json.Unmarshal(resp.Data, &analysis); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &analysis, nil
}

// SkeletonMap returns a token-optimized skeleton of the project.
// detailLevel: "minimal", "standard", or "extended" (empty → "standard").
func SkeletonMap(path, detailLevel string) (*SkeletonResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var cDetail *C.char
	if detailLevel != "" {
		cDetail = C.CString(detailLevel)
		defer C.free(unsafe.Pointer(cDetail))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_skeleton_map(cPath, cDetail)
	})
	if err != nil {
		return nil, err
	}

	var result SkeletonResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// GitChurn returns per-file commit counts over the last `limit` commits.
// Pass limit=0 to use the default (500). Returns an empty map outside a git repo.
func GitChurn(path string, limit uint32) (map[string]int, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_git_churn(cPath, C.uint(limit))
	})
	if err != nil {
		return nil, err
	}

	var churn map[string]int
	if err := json.Unmarshal(resp.Data, &churn); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return churn, nil
}

// GitCochange returns temporally coupled file pairs from the last `limit` commits.
// Pass limit=0 for default (500), minCount=0 for default (2).
func GitCochange(path string, limit, minCount uint32) ([]CoChangePair, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_git_cochange(cPath, C.uint(limit), C.uint(minCount))
	})
	if err != nil {
		return nil, err
	}

	var pairs []CoChangePair
	if err := json.Unmarshal(resp.Data, &pairs); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return pairs, nil
}

// HiddenCoupling returns file pairs that co-change frequently but share no
// import edge — implicit coupling invisible in the static dependency graph.
// Pass limit=0 for default (500), minCount=0 for default (2).
func HiddenCoupling(path string, limit, minCount uint32) ([]CoChangePair, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_hidden_coupling(cPath, C.uint(limit), C.uint(minCount))
	})
	if err != nil {
		return nil, err
	}

	var pairs []CoChangePair
	if err := json.Unmarshal(resp.Data, &pairs); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return pairs, nil
}

// Semidiff returns a function-level diff between two commits.
// commit2 may be empty to default to HEAD.
func Semidiff(path, commit1, commit2 string) ([]SemidiffFile, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cC1 := C.CString(commit1)
	defer C.free(unsafe.Pointer(cC1))

	var cC2 *C.char
	if commit2 != "" {
		cC2 = C.CString(commit2)
		defer C.free(unsafe.Pointer(cC2))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_semidiff(cPath, cC1, cC2)
	})
	if err != nil {
		return nil, err
	}

	var files []SemidiffFile
	if err := json.Unmarshal(resp.Data, &files); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return files, nil
}

// RankedSkeleton returns project files ranked by PageRank relevance, pruned to a token budget.
// focus is the list of files to personalize around; budget is max tokens (0 = unlimited).
func RankedSkeleton(path string, focus []string, budget uint32) (*RankedSkeletonResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	focusJSON, _ := json.Marshal(focus)
	cFocus := C.CString(string(focusJSON))
	defer C.free(unsafe.Pointer(cFocus))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_ranked_skeleton(cPath, cFocus, C.uint(budget))
	})
	if err != nil {
		return nil, err
	}

	// The FFI returns a bare array (lib.rs codecartographer_ranked_skeleton);
	// accept a {"files": [...]} object too in case the envelope ever grows.
	var result RankedSkeletonResult
	if err := json.Unmarshal(resp.Data, &result.Files); err != nil {
		if err2 := json.Unmarshal(resp.Data, &result); err2 != nil {
			return nil, &CartographerError{err.Error()}
		}
	}
	return &result, nil
}

// UnreferencedSymbols returns public symbols that appear unreferenced across the project.
func UnreferencedSymbols(path string) (*UnreferencedSymbolsResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_unreferenced_symbols(cPath)
	})
	if err != nil {
		return nil, err
	}

	var result UnreferencedSymbolsResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// SearchContent searches for a regex or literal pattern across all non-noise project files.
// opts may be nil to use defaults (case-sensitive, unlimited results, no glob filter).
func SearchContent(path, pattern string, opts *SearchContentOptions) (*SearchResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cPattern := C.CString(pattern)
	defer C.free(unsafe.Pointer(cPattern))

	var cOpts *C.char
	if opts != nil {
		b, err := json.Marshal(opts)
		if err != nil {
			return nil, &CartographerError{err.Error()}
		}
		cOpts = C.CString(string(b))
		defer C.free(unsafe.Pointer(cOpts))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_search_content(cPath, cPattern, cOpts)
	})
	if err != nil {
		return nil, err
	}

	var result SearchResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// FindFiles finds files whose repo-relative path matches a glob pattern.
// limit=0 means unlimited. opts may be nil to use defaults.
func FindFiles(path, pattern string, limit uint32, opts *FindOptions) (*FindResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cPattern := C.CString(pattern)
	defer C.free(unsafe.Pointer(cPattern))

	var cOpts *C.char
	if opts != nil {
		b, err := json.Marshal(opts)
		if err != nil {
			return nil, &CartographerError{err.Error()}
		}
		cOpts = C.CString(string(b))
		defer C.free(unsafe.Pointer(cOpts))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_find_files(cPath, cPattern, C.uint(limit), cOpts)
	})
	if err != nil {
		return nil, err
	}

	var result FindResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// GetModuleContext returns a single module's skeleton with dependency info.
func GetModuleContext(path, moduleID string, depth uint32) (*ModuleContext, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cModule := C.CString(moduleID)
	defer C.free(unsafe.Pointer(cModule))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_module_context(cPath, cModule, C.uint(depth))
	})
	if err != nil {
		return nil, err
	}

	var ctx ModuleContext
	if err := json.Unmarshal(resp.Data, &ctx); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &ctx, nil
}

// ReplaceContent performs a regex find-and-replace across project files.
// replacement supports $0 (whole match) and $1/$2 (capture groups).
// When opts.DryRun is true, no files are written.
func ReplaceContent(path, pattern, replacement string, opts *ReplaceOptions) (*ReplaceResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cPattern := C.CString(pattern)
	defer C.free(unsafe.Pointer(cPattern))

	cReplacement := C.CString(replacement)
	defer C.free(unsafe.Pointer(cReplacement))

	var cOpts *C.char
	if opts != nil {
		b, err := json.Marshal(opts)
		if err != nil {
			return nil, &CartographerError{err.Error()}
		}
		cOpts = C.CString(string(b))
		defer C.free(unsafe.Pointer(cOpts))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_replace_content(cPath, cPattern, cReplacement, cOpts)
	})
	if err != nil {
		return nil, err
	}

	var result ReplaceResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// ExtractContent extracts capture-group values from regex matches across project files.
func ExtractContent(path, pattern string, opts *ExtractOptions) (*ExtractResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cPattern := C.CString(pattern)
	defer C.free(unsafe.Pointer(cPattern))

	var cOpts *C.char
	if opts != nil {
		b, err := json.Marshal(opts)
		if err != nil {
			return nil, &CartographerError{err.Error()}
		}
		cOpts = C.CString(string(b))
		defer C.free(unsafe.Pointer(cOpts))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_extract_content(cPath, cPattern, cOpts)
	})
	if err != nil {
		return nil, err
	}

	var result ExtractResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// BM25Search ranks project files by BM25 relevance to a natural-language query.
// opts may be nil to use defaults (k1=1.5, b=0.75, max 20 results).
func BM25Search(path, query string, opts *BM25Options) (*BM25Result, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))

	var cOpts *C.char
	if opts != nil {
		b, err := json.Marshal(opts)
		if err != nil {
			return nil, &CartographerError{err.Error()}
		}
		cOpts = C.CString(string(b))
		defer C.free(unsafe.Pointer(cOpts))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_bm25_search(cPath, cQuery, cOpts)
	})
	if err != nil {
		return nil, err
	}

	var result BM25Result
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// QueryContext runs the full PKG retrieval pipeline: BM25+regex search →
// personalized PageRank skeleton → context health. Returns a ready-to-inject
// context bundle. opts may be nil to use defaults (8000 token budget, claude model).
func QueryContext(path, query string, opts *QueryContextOpts) (*QueryContextResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))

	var cOpts *C.char
	if opts != nil {
		b, err := json.Marshal(opts)
		if err != nil {
			return nil, &CartographerError{err.Error()}
		}
		cOpts = C.CString(string(b))
		defer C.free(unsafe.Pointer(cOpts))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_query_context(cPath, cQuery, cOpts)
	})
	if err != nil {
		return nil, err
	}

	var result QueryContextResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// ShotgunSurgery returns files ranked by co-change dispersion.
// limit=0 uses the cartographer default (100). minPartners=0 uses the default (3).
// Files with a high dispersion score exhibit the shotgun surgery smell: changing them
// historically required simultaneous changes across many unrelated files.
func ShotgunSurgery(path string, limit, minPartners uint32) ([]ShotgunSurgeryEntry, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_shotgun_surgery(cPath, C.uint(limit), C.uint(minPartners))
	})
	if err != nil {
		return nil, err
	}

	var entries []ShotgunSurgeryEntry
	if err := json.Unmarshal(resp.Data, &entries); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return entries, nil
}

// Evolution returns architectural health snapshots over the last `days` days of git history.
// days=0 uses the cartographer default (90).
func Evolution(path string, days uint32) (*EvolutionResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_evolution(cPath, C.uint(days))
	})
	if err != nil {
		return nil, err
	}

	var result EvolutionResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// BlastRadius returns the graph-theoretic blast radius for a module/file target.
// target is a repo-relative file path or module ID. maxRelated=0 uses the default (50).
func BlastRadius(path, target string, maxRelated uint32) (*BlastRadiusResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cTarget := C.CString(target)
	defer C.free(unsafe.Pointer(cTarget))

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_blast_radius(cPath, cTarget, C.uint(maxRelated))
	})
	if err != nil {
		return nil, err
	}

	var result BlastRadiusResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// ContextHealth analyses the quality of an LLM context bundle and returns a
// health report with a composite score (0–100, graded A–F) and per-metric
// breakdown. opts may be nil to use defaults (Claude 200K window).
func ContextHealth(content string, opts *ContextHealthOpts) (*ContextHealthReport, error) {
	cContent := C.CString(content)
	defer C.free(unsafe.Pointer(cContent))

	var cOpts *C.char
	if opts != nil {
		b, err := json.Marshal(opts)
		if err != nil {
			return nil, &CartographerError{err.Error()}
		}
		cOpts = C.CString(string(b))
		defer C.free(unsafe.Pointer(cOpts))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_context_health(cContent, cOpts)
	})
	if err != nil {
		return nil, err
	}

	var result ContextHealthReport
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}

// RenderArchitecture renders the project's import graph as a Mermaid or
// Graphviz (DOT) diagram. focus is an optional module_id or path suffix —
// when set, the diagram is a BFS neighborhood of that module up to `depth`;
// when empty, it's the top-N nodes by degree. depth=0 → 2, maxNodes=0 → 40.
func RenderArchitecture(path, format, focus string, depth, maxNodes uint32) (*RenderArchitectureResult, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var cFormat *C.char
	if format != "" {
		cFormat = C.CString(format)
		defer C.free(unsafe.Pointer(cFormat))
	}

	var cFocus *C.char
	if focus != "" {
		cFocus = C.CString(focus)
		defer C.free(unsafe.Pointer(cFocus))
	}

	resp, err := callFFI(func() *C.char {
		return C.codecartographer_render_architecture(cPath, cFormat, cFocus, C.uint(depth), C.uint(maxNodes))
	})
	if err != nil {
		return nil, err
	}

	var result RenderArchitectureResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, &CartographerError{err.Error()}
	}
	return &result, nil
}
