// Package perf detects performance and structural health issues in a codebase:
// loop call sites in hot files (AnalyzeStructural), and the types of the
// hidden-coupling scan, which runs in the query engine because it needs the
// SCIP index (query.Engine.ScanHiddenCoupling).
package perf

// ScanOptions configures a hidden-coupling scan (query.Engine.ScanHiddenCoupling).
// The history is coupling.History: the last 500 commits, bot, formatting and
// oversized commits dropped.
type ScanOptions struct {
	// Scope limits analysis to pairs whose files both lie under one of these
	// paths (relative to repo root). Empty means whole repo.
	Scope []string

	// MinCorrelation is the minimum co-change score to report (0–1): shared
	// commits / (churn of the less-changed file + 2), see coupling.Score.
	// Default: 0.3
	MinCorrelation float64

	// MinCoChanges is the minimum absolute number of shared commits.
	// Filters out spurious pairs from low-activity files. Default: 3
	MinCoChanges int

	// Limit caps the number of hidden-coupling pairs returned. Default: 50
	Limit int
}

// HiddenCouplingPair represents two source files that co-change without any
// static link CKB sees in the SCIP index: neither references a symbol the
// other defines, and they are not files of one Go package.
type HiddenCouplingPair struct {
	// FileA and FileB are repo-relative paths of the coupled files.
	FileA string `json:"fileA"`
	FileB string `json:"fileB"`

	// Correlation is the co-change score, coupling.Score.
	Correlation float64 `json:"correlation"`

	// CoChangeCount is the raw number of commits that touched both files.
	CoChangeCount int `json:"coChangeCount"`

	// Level is "high" (≥0.7), "medium" (≥0.5), or "low".
	Level string `json:"level"`

	// Explanation is a human-readable description of why this is notable.
	Explanation string `json:"explanation"`
}

// PerfScanSummary aggregates the scan results.
type PerfScanSummary struct {
	// CommitsRead is the number of commits the co-change history counts.
	CommitsRead   int `json:"commitsRead"`
	FilesObserved int `json:"filesObserved"`
	// PairsChecked is the number of pairs that passed the thresholds and went
	// through the link check.
	PairsChecked int `json:"pairsChecked"`
	// PairsNotIndexed is how many of those were skipped because a file is not
	// source code in the index: its links are unknown, not absent.
	PairsNotIndexed  int `json:"pairsNotIndexed"`
	HiddenPairsFound int `json:"hiddenPairsFound"`
	// NotAssessed says why no pair was checked (no SCIP index); empty otherwise.
	NotAssessed string `json:"notAssessed,omitempty"`
}

// PerfScanResult is the output of a perf scan.
type PerfScanResult struct {
	HiddenCoupling []HiddenCouplingPair `json:"hiddenCoupling"`
	Summary        PerfScanSummary      `json:"summary"`
}

// CorrelationLevel names a co-change score: "high" from 0.7 (5 of 5 shared
// commits), "medium" from 0.5, "low" below.
func CorrelationLevel(c float64) string {
	switch {
	case c >= 0.7:
		return "high"
	case c >= 0.5:
		return "medium"
	default:
		return "low"
	}
}

// StructuralPerfOptions configures a structural performance scan.
type StructuralPerfOptions struct {
	// Scope limits analysis to these paths (relative to RepoRoot). Empty = whole repo.
	Scope []string
	// Limit caps the number of loop call sites returned. Default: 100.
	Limit int
	// WindowDays is the git history window to consider. Default: 90.
	WindowDays int
	// MinChurnCount is the minimum commit count for a file to be considered hot. Default: 3.
	MinChurnCount int
	// EntrypointFiles are repo-relative paths of known system entrypoints.
	// Loop call sites in these files are marked NearEntrypoint and ranked higher.
	EntrypointFiles []string
}

// LoopCallSite represents a function call expression found inside a loop body
// in a high-churn file. These are the primary structural signal for O(n) or
// O(n²) hidden costs that do not appear in profiling until production load.
type LoopCallSite struct {
	File           string `json:"file"`           // repo-relative path
	Line           int    `json:"line"`           // 1-indexed line of the call expression
	FunctionName   string `json:"functionName"`   // enclosing function/method name
	CallText       string `json:"callText"`       // call expression text (truncated to 120 chars)
	LoopType       string `json:"loopType"`       // "for", "range", "while", "do-while", "loop"
	ChurnCount     int    `json:"churnCount"`     // commits touching this file in the window
	NearEntrypoint bool   `json:"nearEntrypoint"` // true if file is a known system entrypoint
	Severity       string `json:"severity"`       // "high", "medium", "low"
	Explanation    string `json:"explanation"`    // human-readable description
}

// StructuralPerfSummary aggregates the structural scan results.
type StructuralPerfSummary struct {
	FilesScanned   int `json:"filesScanned"`
	HotFilesFound  int `json:"hotFilesFound"`
	CallSitesFound int `json:"callSitesFound"`
}

// StructuralPerfResult is the output of a structural performance scan.
type StructuralPerfResult struct {
	LoopCallSites []LoopCallSite        `json:"loopCallSites"`
	Summary       StructuralPerfSummary `json:"summary"`
	// NoCGO is true when tree-sitter analysis was unavailable (non-CGO build).
	NoCGO bool `json:"noCGO,omitempty"`
}
