// Package coupling provides co-change pattern analysis for files and symbols.
// It detects which files historically change together based on git commit history.
package coupling

import "time"

// Correlation represents a correlation between two files
type Correlation struct {
	// What changes with the target
	Symbol   string `json:"symbol,omitempty"`
	File     string `json:"file"`               // Repo-relative path
	FilePath string `json:"filePath,omitempty"` // Same as File, kept for existing consumers

	// Correlation strength
	Correlation   float64 `json:"correlation"`   // 0-1
	CoChangeCount int     `json:"coChangeCount"` // times changed together
	TotalChanges  int     `json:"totalChanges"`  // times target changed

	// Direction (optional)
	Direction string `json:"direction,omitempty"` // "bidirectional" | "target_leads" | "target_follows"

	// Classification
	Level string `json:"level"` // "high" | "medium" | "low"
}

// CouplingAnalysis represents the result of a coupling analysis
type CouplingAnalysis struct {
	Target struct {
		Symbol         string `json:"symbol,omitempty"`
		File           string `json:"file"`
		CommitCount    int    `json:"commitCount"`
		AnalysisWindow struct {
			From time.Time `json:"from"`
			To   time.Time `json:"to"`
		} `json:"analysisWindow"`
	} `json:"target"`

	Correlations    []Correlation `json:"correlations"`
	Insights        []string      `json:"insights"`
	Recommendations []string      `json:"recommendations"`
}

// AnalyzeOptions configures the coupling analysis
type AnalyzeOptions struct {
	RepoRoot       string  // Repository root path
	Target         string  // File or symbol to analyze
	MinCorrelation float64 // Minimum correlation threshold (default: 0.3)
	WindowDays     int     // Analysis window in days (default: 365)
	Limit          int     // Max results to return (default: 20)
}

// CachedCorrelation represents a cached correlation entry in the database
type CachedCorrelation struct {
	FilePath       string    `json:"filePath"`
	CorrelatedFile string    `json:"correlatedFile"`
	Correlation    float64   `json:"correlation"`
	CoChangeCount  int       `json:"coChangeCount"`
	TotalChanges   int       `json:"totalChanges"`
	ComputedAt     time.Time `json:"computedAt"`
}

// GetCorrelationLevel returns the correlation level based on the value
func GetCorrelationLevel(correlation float64) string {
	switch {
	case correlation >= 0.8:
		return "high"
	case correlation >= 0.5:
		return "medium"
	default:
		return "low"
	}
}
