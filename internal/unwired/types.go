package unwired

// UnwiredItem represents an exported symbol that is never transitively
// reachable from any application entrypoint.
type UnwiredItem struct {
	SymbolID       string  `json:"symbolId"`
	SymbolName     string  `json:"symbolName"`
	Kind           string  `json:"kind"`
	FilePath       string  `json:"filePath"`
	LineNumber     int     `json:"lineNumber,omitempty"`
	Module         string  `json:"module"`
	ReferenceCount int     `json:"referenceCount"`
	TestReferences int     `json:"testReferences,omitempty"`
	Confidence     float64 `json:"confidence"`
	Reason         string  `json:"reason"`
	Exported       bool    `json:"exported"`
}

// UnwiredModule groups unwired items by directory/module.
type UnwiredModule struct {
	Path    string        `json:"path"`
	Items   []UnwiredItem `json:"items"`
	Summary ModuleSummary `json:"summary"`
}

// ModuleSummary provides per-module aggregate stats.
type ModuleSummary struct {
	TotalExported int `json:"totalExported"`
	UnwiredCount  int `json:"unwiredCount"`
}

// Result is the output of the unwired module detector.
type Result struct {
	UnwiredModules []UnwiredModule `json:"unwiredModules"`
	Summary        Summary         `json:"summary"`
	Entrypoints    []string        `json:"entrypoints"`
	ReachableCount int             `json:"reachableCount"`
	Partial        bool            `json:"partial,omitempty"`
	// Truncated is set when the limit cut the list; Summary has the full counts.
	Truncated bool `json:"truncated,omitempty"`
}

// Summary provides aggregate statistics.
type Summary struct {
	TotalExported  int `json:"totalExported"`
	ReachableCount int `json:"reachableCount"`
	UnwiredCount   int `json:"unwiredCount"`
	UnwiredModules int `json:"unwiredModules"`
	// ViaInterface counts the methods not listed because they implement a
	// reachable interface on a reachable type.
	ViaInterface int            `json:"viaInterface,omitempty"`
	ByKind       map[string]int `json:"byKind"`
}

// DetectorOptions configures the unwired module detector.
type DetectorOptions struct {
	Scope           []string `json:"scope,omitempty"`
	ExcludePatterns []string `json:"excludePatterns,omitempty"`
	MaxNodes        int      `json:"maxNodes"`
	MinConfidence   float64  `json:"minConfidence"`
	IncludeTypes    bool     `json:"includeTypes"`
	Limit           int      `json:"limit"`
}
