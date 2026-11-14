package query

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/output"
	"github.com/SimplyLiz/CodeMCP/internal/responsibilities"
)

// GetModuleResponsibilitiesOptions controls getModuleResponsibilities behavior.
type GetModuleResponsibilitiesOptions struct {
	ModuleId     string // Specific module, or empty for all
	IncludeFiles bool   // Include file-level responsibilities
	Limit        int    // Max modules to return
}

// ModuleResponsibility represents a module's responsibility info.
type ModuleResponsibility struct {
	ModuleId     string               `json:"moduleId"`
	Name         string               `json:"name"`
	Path         string               `json:"path"`
	Summary      string               `json:"summary"`
	Capabilities []string             `json:"capabilities,omitempty"`
	Source       string               `json:"source"` // "declared" | "inferred"
	Confidence   float64              `json:"confidence"`
	Files        []FileResponsibility `json:"files,omitempty"`
}

// FileResponsibility represents a file's responsibility info.
type FileResponsibility struct {
	Path       string  `json:"path"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
}

// GetModuleResponsibilitiesResponse is the response for getModuleResponsibilities.
type GetModuleResponsibilitiesResponse struct {
	CkbVersion      string                 `json:"ckbVersion"`
	SchemaVersion   string                 `json:"schemaVersion"`
	Tool            string                 `json:"tool"`
	Modules         []ModuleResponsibility `json:"modules"`
	TotalCount      int                    `json:"totalCount"`
	Confidence      float64                `json:"confidence"`
	ConfidenceBasis []ConfidenceBasisItem  `json:"confidenceBasis"`
	Limitations     []string               `json:"limitations,omitempty"`
	Provenance      *Provenance            `json:"provenance,omitempty"`
	Drilldowns      []output.Drilldown     `json:"drilldowns,omitempty"`
}

// GetModuleResponsibilities returns responsibilities for modules.
func (e *Engine) GetModuleResponsibilities(ctx context.Context, opts GetModuleResponsibilitiesOptions) (*GetModuleResponsibilitiesResponse, error) {
	startTime := time.Now()

	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	var confidenceBasis []ConfidenceBasisItem
	var limitations []string
	var moduleResponsibilities []ModuleResponsibility

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		return nil, err
	}

	// Create responsibility extractor
	extractor := responsibilities.NewExtractor(e.repoRoot)

	// Get modules from architecture
	archOpts := GetArchitectureOptions{Depth: 2}
	archResp, err := e.GetArchitecture(ctx, archOpts)
	if err != nil {
		limitations = append(limitations, "Could not get architecture: "+err.Error())
	}

	confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
		Backend: "responsibility-extractor",
		Status:  "available",
	})

	// If specific module requested, filter
	if opts.ModuleId != "" && archResp != nil {
		for _, m := range archResp.Modules {
			if m.ModuleId == opts.ModuleId || m.Path == opts.ModuleId {
				modResp, ok := e.buildModuleResponsibility(m.ModuleId, m.Name, m.Path, extractor, &limitations)
				if !ok {
					break // extraction failed and no annotations - skip
				}

				// Include file responsibilities if requested
				if opts.IncludeFiles {
					modResp.Files = e.extractFileResponsibilities(extractor, m.Path)
				}

				moduleResponsibilities = append(moduleResponsibilities, modResp)
				break
			}
		}
	} else if archResp != nil {
		// Get all modules
		for _, m := range archResp.Modules {
			if len(moduleResponsibilities) >= opts.Limit {
				break
			}

			modResp, ok := e.buildModuleResponsibility(m.ModuleId, m.Name, m.Path, extractor, &limitations)
			if !ok {
				continue // extraction failed and no annotations - skip
			}

			// Include file responsibilities if requested
			if opts.IncludeFiles {
				modResp.Files = e.extractFileResponsibilities(extractor, m.Path)
			}

			moduleResponsibilities = append(moduleResponsibilities, modResp)
		}
	}

	// Compute overall confidence
	confidence := 0.69 // Base confidence for inferred
	if len(moduleResponsibilities) > 0 {
		var totalConf float64
		for _, m := range moduleResponsibilities {
			totalConf += m.Confidence
		}
		confidence = totalConf / float64(len(moduleResponsibilities))
	}

	// Build completeness
	completeness := CompletenessInfo{
		Score:  1.0,
		Reason: "responsibilities-extracted",
	}
	if len(limitations) > 0 {
		completeness.Score = 0.7
		completeness.Reason = "partial-extraction"
	}

	// Build provenance
	provenance := e.buildProvenance(repoState, "head", startTime, nil, completeness)

	// Generate drilldowns
	var drilldowns []output.Drilldown
	if len(moduleResponsibilities) > 0 {
		drilldowns = append(drilldowns, output.Drilldown{
			Label:          "Get architecture",
			Query:          "getArchitecture",
			RelevanceScore: 0.8,
		})
		drilldowns = append(drilldowns, output.Drilldown{
			Label:          "Get ownership",
			Query:          "getOwnership for " + moduleResponsibilities[0].Path,
			RelevanceScore: 0.7,
		})
	}

	return &GetModuleResponsibilitiesResponse{
		CkbVersion:      "6.0",
		SchemaVersion:   "6.0",
		Tool:            "getModuleResponsibilities",
		Modules:         moduleResponsibilities,
		TotalCount:      len(moduleResponsibilities),
		Confidence:      confidence,
		ConfidenceBasis: confidenceBasis,
		Limitations:     limitations,
		Provenance:      provenance,
		Drilldowns:      drilldowns,
	}, nil
}

// extractFileResponsibilities extracts responsibilities for files in a module
func (e *Engine) extractFileResponsibilities(extractor *responsibilities.Extractor, modulePath string) []FileResponsibility {
	var files []FileResponsibility

	fullPath := filepath.Join(e.repoRoot, modulePath)
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return files
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only process source files
		name := entry.Name()
		if !isSourceFile(name) {
			continue
		}

		filePath := filepath.Join(modulePath, name)
		resp, err := extractor.ExtractFromFile(filePath)
		if err != nil {
			continue
		}

		files = append(files, FileResponsibility{
			Path:       filePath,
			Summary:    resp.Summary,
			Confidence: resp.Confidence,
		})

		// Limit files per module
		if len(files) >= 10 {
			break
		}
	}

	return files
}

// isSourceFile checks if a file is a source code file
func isSourceFile(name string) bool {
	sourceExtensions := map[string]bool{
		".go":   true,
		".ts":   true,
		".tsx":  true,
		".js":   true,
		".jsx":  true,
		".py":   true,
		".rs":   true,
		".java": true,
		".kt":   true,
		".dart": true,
	}
	ext := filepath.Ext(name)
	return sourceExtensions[ext]
}

// isConceptSourceFile reports whether a path is source code worth naming
// concepts after: the languages isSourceFile knows plus the other ones CKB
// indexes. Data, docs and config files are not.
func isConceptSourceFile(path string) bool {
	if isSourceFile(path) {
		return true
	}
	switch filepath.Ext(path) {
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".cs", ".rb", ".swift", ".php", ".scala", ".mjs", ".cjs", ".vue", ".svelte":
		return true
	}
	return false
}

// buildModuleResponsibility creates a ModuleResponsibility, preferring declared annotations over inferred.
// v6.5: Declared annotations take priority with higher confidence.
// Returns (result, ok) where ok=false means both extraction failed and no annotations exist,
// so the caller should skip this module to avoid polluting results with empty entries.
func (e *Engine) buildModuleResponsibility(moduleId, name, path string, extractor *responsibilities.Extractor, limitations *[]string) (ModuleResponsibility, bool) {
	modResp := ModuleResponsibility{
		ModuleId: moduleId,
		Name:     name,
		Path:     path,
	}

	// v6.5: Check for declared annotations first
	annotations := e.getModuleAnnotations(moduleId)
	if annotations == nil {
		// Also try with path as moduleId (common pattern)
		annotations = e.getModuleAnnotations(path)
	}

	// If we have declared annotations, use them with high confidence
	if annotations != nil && annotations.Source == "declared" {
		modResp.Summary = annotations.Responsibility
		modResp.Capabilities = annotations.Capabilities
		modResp.Source = "declared"
		modResp.Confidence = annotations.Confidence
		return modResp, true
	}

	// Fall back to inference
	resp, respErr := extractor.ExtractFromModule(path)
	if respErr != nil {
		if limitations != nil {
			*limitations = append(*limitations, "Failed to extract from "+path+": "+respErr.Error())
		}
		// Return what we have from annotations (if any)
		if annotations != nil {
			modResp.Summary = annotations.Responsibility
			modResp.Capabilities = annotations.Capabilities
			modResp.Source = annotations.Source
			modResp.Confidence = annotations.Confidence
			return modResp, true
		}
		// No annotations and extraction failed - skip this module
		return modResp, false
	}

	// Use inferred data
	modResp.Summary = resp.Summary
	modResp.Capabilities = resp.Capabilities
	modResp.Source = resp.Source
	modResp.Confidence = resp.Confidence

	// Merge with annotations if available (declared takes priority for specific fields)
	if annotations != nil {
		// If annotation has responsibility, prefer it
		if annotations.Responsibility != "" {
			modResp.Summary = annotations.Responsibility
			// Boost confidence when we have both declared and inferred
			modResp.Source = "declared+inferred"
			modResp.Confidence = (annotations.Confidence + resp.Confidence) / 2
			if modResp.Confidence < annotations.Confidence {
				modResp.Confidence = annotations.Confidence
			}
		}
		// Merge capabilities (annotations take priority)
		if len(annotations.Capabilities) > 0 {
			modResp.Capabilities = annotations.Capabilities
		}
	}

	return modResp, true
}
