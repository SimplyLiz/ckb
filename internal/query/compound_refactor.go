package query

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/audit"
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// PlanRefactorOptions configures the unified refactoring planner.
type PlanRefactorOptions struct {
	Target     string     // file or symbol
	ChangeType ChangeType // modify, rename, delete, extract, move
	TargetPath string     // destination path (for move operations)
	StartLine  int        // start line for extract operations (0 = whole file)
	EndLine    int        // end line for extract operations (0 = whole file)
}

// PlanRefactorResponse contains the combined result of all refactoring analysis.
type PlanRefactorResponse struct {
	AINavigationMeta
	Target           *PrepareChangeTarget  `json:"target"`
	RiskAssessment   *PlanRefactorRisk     `json:"riskAssessment"`
	ImpactAnalysis   *PlanRefactorImpact   `json:"impactAnalysis"`
	TestStrategy     *PlanRefactorTests    `json:"testStrategy"`
	CouplingAnalysis *PlanRefactorCoupling `json:"couplingAnalysis,omitempty"`
	RefactoringSteps []RefactoringStep     `json:"refactoringSteps"`
}

// PlanRefactorRisk combines file-level risk with per-function complexity breakdown.
type PlanRefactorRisk struct {
	RiskLevel          string               `json:"riskLevel"` // critical, high, medium, low, or RiskLevelNotAssessed
	RiskScore          float64              `json:"riskScore"` // 0-100
	Factors            []audit.RiskFactor   `json:"factors,omitempty"`
	FunctionComplexity []audit.FunctionRisk `json:"functionComplexity,omitempty"`
}

// PlanRefactorImpact summarizes the blast radius.
type PlanRefactorImpact struct {
	DirectDependents  int    `json:"directDependents"`
	TransitiveClosure int    `json:"transitiveClosure"`
	AffectedFiles     int    `json:"affectedFiles"`
	RenamePreview     string `json:"renamePreview,omitempty"` // only for rename
}

// PlanRefactorTests describes what tests exist and what's missing.
type PlanRefactorTests struct {
	ExistingTests   int     `json:"existingTests"`
	TestGapCount    int     `json:"testGapCount"`
	CoveragePercent float64 `json:"coveragePercent"`
	HighestRiskGap  string  `json:"highestRiskGap,omitempty"` // function name of riskiest untested fn
}

// PlanRefactorCoupling describes co-change coupling for the target.
type PlanRefactorCoupling struct {
	CoChangeFiles       int      `json:"coChangeFiles"`
	HighestCoupled      string   `json:"highestCoupled,omitempty"`
	HiddenCouplingFiles []string `json:"hiddenCouplingFiles,omitempty"` // co-change without import edge
}

// RefactoringStep is an ordered action in the refactoring plan.
type RefactoringStep struct {
	Order       int    `json:"order"`
	Action      string `json:"action"`
	Description string `json:"description"`
	Risk        string `json:"risk"` // "low", "medium", "high"
}

// PlanRefactor executes a comprehensive refactoring plan by running
// prepareChange, auditRisk, and analyzeTestGaps in parallel.
func (e *Engine) PlanRefactor(ctx context.Context, opts PlanRefactorOptions) (*PlanRefactorResponse, error) {
	startTime := time.Now()

	if opts.ChangeType == "" {
		opts.ChangeType = ChangeModify
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "full")
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// Sub-query results
	var prepareResult *PrepareChangeResponse
	var riskItem *audit.RiskItem
	var testGapResult *PlanRefactorTests
	var warnings []string

	// 1. PrepareChange (impact, dependents, tests, co-changes)
	wg.Add(1)
	go func() {
		defer wg.Done()
		result, err := e.PrepareChange(ctx, PrepareChangeOptions(opts))
		if err != nil {
			mu.Lock()
			warnings = append(warnings, fmt.Sprintf("prepareChange: %s", err.Error()))
			mu.Unlock()
			return
		}
		mu.Lock()
		prepareResult = result
		mu.Unlock()
	}()

	// 2. AuditRisk on the target file for risk + function breakdown
	wg.Add(1)
	go func() {
		defer wg.Done()
		filePath := opts.Target
		// If target is a symbol ID, we'll get the path from prepareResult later
		if filepath.Ext(filePath) == "" && filePath != "" {
			// Likely a symbol ID, skip file-level audit for now
			return
		}
		analyzer := audit.NewAnalyzer(e.repoRoot, e.logger)
		analysisResult, err := analyzer.Analyze(ctx, audit.AuditOptions{
			RepoRoot: e.repoRoot,
			MinScore: 0, // include everything for this specific file
			Limit:    1,
		})
		if err != nil {
			mu.Lock()
			warnings = append(warnings, fmt.Sprintf("auditRisk: %s", err.Error()))
			mu.Unlock()
			return
		}
		if analysisResult != nil && len(analysisResult.Items) > 0 {
			// Find the item matching our target
			for i, item := range analysisResult.Items {
				if item.File == filePath || filepath.Base(item.File) == filepath.Base(filePath) {
					mu.Lock()
					riskItem = &analysisResult.Items[i]
					mu.Unlock()
					return
				}
			}
		}
	}()

	// 3. Test gap analysis
	wg.Add(1)
	go func() {
		defer wg.Done()
		result, err := e.AnalyzeTestGaps(ctx, AnalyzeTestGapsOptions{
			Target:   opts.Target,
			MinLines: 3,
			Limit:    20,
		})
		if err != nil {
			mu.Lock()
			warnings = append(warnings, fmt.Sprintf("testGaps: %s", err.Error()))
			mu.Unlock()
			return
		}
		if result != nil {
			strategy := &PlanRefactorTests{
				TestGapCount:    len(result.Gaps),
				CoveragePercent: result.Summary.CoveragePercent,
				ExistingTests:   result.Summary.TestedFunctions,
			}
			if len(result.Gaps) > 0 {
				strategy.HighestRiskGap = result.Gaps[0].Function
			}
			mu.Lock()
			testGapResult = strategy
			mu.Unlock()
		}
	}()

	wg.Wait()

	// Assemble response
	resp := &PlanRefactorResponse{}

	// Target from prepareChange
	if prepareResult != nil {
		resp.Target = prepareResult.Target

		// Impact analysis
		resp.ImpactAnalysis = &PlanRefactorImpact{
			DirectDependents: len(prepareResult.DirectDependents),
		}
		if prepareResult.TransitiveImpact != nil {
			resp.ImpactAnalysis.TransitiveClosure = prepareResult.TransitiveImpact.TotalCallers
			resp.ImpactAnalysis.AffectedFiles = prepareResult.TransitiveImpact.ModuleSpread
		}
		if prepareResult.RenameDetail != nil {
			resp.ImpactAnalysis.RenamePreview = FormatRenamePreview(prepareResult.RenameDetail)
		}

		// Coupling — include explicit co-change count and highest coupled file.
		if len(prepareResult.CoChangeFiles) > 0 {
			resp.CouplingAnalysis = &PlanRefactorCoupling{
				CoChangeFiles: len(prepareResult.CoChangeFiles),
			}
			if len(prepareResult.CoChangeFiles) > 0 {
				resp.CouplingAnalysis.HighestCoupled = prepareResult.CoChangeFiles[0].File
			}
			// Collect hidden-coupling files (co-change without import edge) from the
			// PrepareCoChange results — these are the highest-risk implicit dependencies
			// for a refactoring because they will cascade without any static signal.
			for _, cc := range prepareResult.CoChangeFiles {
				if cc.IsHidden {
					resp.CouplingAnalysis.HiddenCouplingFiles = append(
						resp.CouplingAnalysis.HiddenCouplingFiles, cc.File,
					)
				}
			}
		}

		// Augment with Cartographer hidden coupling when not already captured above
		// (e.g. when Cartographer was unavailable during PrepareChange but is now).
		if cartographer.Available() && resp.CouplingAnalysis != nil && len(resp.CouplingAnalysis.HiddenCouplingFiles) == 0 {
			filePath := opts.Target
			if prepareResult.Target != nil && prepareResult.Target.Path != "" {
				filePath = prepareResult.Target.Path
			}
			if hidden, err := cartographer.HiddenCoupling(e.repoRoot, 0, 2); err == nil {
				for _, p := range hidden {
					partner := ""
					if p.FileA == filePath {
						partner = p.FileB
					} else if p.FileB == filePath {
						partner = p.FileA
					}
					if partner != "" {
						resp.CouplingAnalysis.HiddenCouplingFiles = append(
							resp.CouplingAnalysis.HiddenCouplingFiles, partner,
						)
					}
				}
			}
		}
	}

	// Risk assessment
	resp.RiskAssessment = &PlanRefactorRisk{
		RiskLevel: RiskLevelNotAssessed, // Until one of the two analyses below says otherwise
		RiskScore: 0,
	}
	if riskItem != nil {
		resp.RiskAssessment.RiskLevel = riskItem.RiskLevel
		resp.RiskAssessment.RiskScore = riskItem.RiskScore
		resp.RiskAssessment.Factors = riskItem.Factors
		resp.RiskAssessment.FunctionComplexity = riskItem.FunctionComplexity
	} else if prepareResult != nil && prepareResult.RiskAssessment != nil {
		// Fall back to prepareChange risk if file-level audit didn't run
		resp.RiskAssessment.RiskLevel = prepareResult.RiskAssessment.Level
		resp.RiskAssessment.RiskScore = prepareResult.RiskAssessment.Score
	}

	// Test strategy
	if testGapResult != nil {
		resp.TestStrategy = testGapResult
	} else {
		resp.TestStrategy = &PlanRefactorTests{}
	}

	// Generate refactoring steps
	resp.RefactoringSteps = generateRefactoringSteps(opts.ChangeType, resp)

	// Build provenance
	var backendContribs []BackendContribution
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId: "scip", Available: true, Used: true,
		})
	}
	if e.gitAdapter != nil && e.gitAdapter.IsAvailable() {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId: "git", Available: true, Used: true,
		})
	}

	resp.AINavigationMeta = AINavigationMeta{
		CkbVersion:    version.Version,
		SchemaVersion: 1,
		Tool:          "planRefactor",
		Provenance: e.buildProvenance(repoState, "full", startTime, backendContribs, CompletenessInfo{
			Score:  0.85,
			Reason: "compound-planrefactor",
		}),
	}

	return resp, nil
}

// generateRefactoringSteps produces ordered steps based on change type and analysis.
func generateRefactoringSteps(changeType ChangeType, resp *PlanRefactorResponse) []RefactoringStep {
	switch changeType {
	case ChangeRename:
		return generateRenameSteps(resp)
	case ChangeExtract:
		return generateExtractSteps(resp)
	case ChangeDelete:
		return generateDeleteSteps(resp)
	case ChangeMove:
		return generateMoveSteps(resp)
	default:
		return generateModifySteps(resp)
	}
}

func generateRenameSteps(resp *PlanRefactorResponse) []RefactoringStep {
	steps := []RefactoringStep{
		{Order: 1, Action: "Update definition", Description: "Rename the symbol at its definition site", Risk: "low"},
	}

	sites := 0
	if resp.ImpactAnalysis != nil {
		sites = resp.ImpactAnalysis.DirectDependents
	}
	steps = append(steps, RefactoringStep{
		Order:       2,
		Action:      "Update call sites",
		Description: fmt.Sprintf("Update %d call site(s) that reference this symbol", sites),
		Risk:        stepRisk(sites, 10, 50),
	})

	steps = append(steps,
		RefactoringStep{Order: 3, Action: "Update imports", Description: "Update any import paths referencing the old name", Risk: "low"},
		RefactoringStep{Order: 4, Action: "Run tests", Description: "Execute affected test suite to verify rename", Risk: "low"},
	)

	return steps
}

func generateExtractSteps(resp *PlanRefactorResponse) []RefactoringStep {
	return []RefactoringStep{
		{Order: 1, Action: "Identify boundary", Description: "Determine extraction boundary and variable flow", Risk: "medium"},
		{Order: 2, Action: "Create function", Description: "Create new function with identified parameters and returns", Risk: "low"},
		{Order: 3, Action: "Replace inline", Description: "Replace original code block with call to new function", Risk: "medium"},
		{Order: 4, Action: "Update tests", Description: "Add tests for extracted function, update existing tests", Risk: "low"},
	}
}

func generateDeleteSteps(resp *PlanRefactorResponse) []RefactoringStep {
	dependents := 0
	if resp.ImpactAnalysis != nil {
		dependents = resp.ImpactAnalysis.DirectDependents
	}
	steps := []RefactoringStep{
		{Order: 1, Action: "Verify unused", Description: fmt.Sprintf("Confirm symbol has %d dependent(s) — resolve before deletion", dependents), Risk: stepRisk(dependents, 1, 5)},
		{Order: 2, Action: "Remove symbol", Description: "Delete the symbol definition", Risk: "low"},
		{Order: 3, Action: "Clean imports", Description: "Remove any now-unused import paths", Risk: "low"},
		{Order: 4, Action: "Run tests", Description: "Execute full test suite to verify no breakage", Risk: "low"},
	}
	return steps
}

func generateModifySteps(resp *PlanRefactorResponse) []RefactoringStep {
	dependents := 0
	if resp.ImpactAnalysis != nil {
		dependents = resp.ImpactAnalysis.DirectDependents
	}
	return []RefactoringStep{
		{Order: 1, Action: "Review dependents", Description: fmt.Sprintf("Review %d direct dependent(s) for compatibility", dependents), Risk: stepRisk(dependents, 5, 20)},
		{Order: 2, Action: "Update implementation", Description: "Apply changes to the target", Risk: "medium"},
		{Order: 3, Action: "Run affected tests", Description: "Execute tests related to modified code", Risk: "low"},
		{Order: 4, Action: "Check coupling", Description: "Verify co-change files don't need updates", Risk: "low"},
	}
}

func generateMoveSteps(resp *PlanRefactorResponse) []RefactoringStep {
	dependents := 0
	if resp.ImpactAnalysis != nil {
		dependents = resp.ImpactAnalysis.DirectDependents
	}
	return []RefactoringStep{
		{Order: 1, Action: "Verify target location", Description: "Check target directory for conflicts and ensure it exists", Risk: "low"},
		{Order: 2, Action: "Move file/symbol", Description: "Relocate the source to the target location", Risk: "low"},
		{Order: 3, Action: "Update imports", Description: fmt.Sprintf("Update import statements across %d dependent file(s)", dependents), Risk: stepRisk(dependents, 10, 50)},
		{Order: 4, Action: "Run affected tests", Description: "Execute tests to verify move didn't break anything", Risk: "low"},
	}
}

// stepRisk returns risk level based on count thresholds.
func stepRisk(count, mediumThreshold, highThreshold int) string {
	if count >= highThreshold {
		return "high"
	}
	if count >= mediumThreshold {
		return "medium"
	}
	return "low"
}
