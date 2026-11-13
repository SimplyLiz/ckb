package impact

import (
	"fmt"
	"math"
)

// RiskLevel represents the risk level of a change
type RiskLevel string

const (
	RiskHigh   RiskLevel = "high"
	RiskMedium RiskLevel = "medium"
	RiskLow    RiskLevel = "low"
)

// RiskScore contains the calculated risk assessment
type RiskScore struct {
	Level       RiskLevel    // Overall risk level
	Score       float64      // Numeric score (0.0 - 1.0)
	Factors     []RiskFactor // Contributing factors
	Explanation string       // Human-readable explanation
}

// RiskFactor represents a single contributing factor to risk
type RiskFactor struct {
	Name   string  // Factor name
	Weight float64 // Weight in the overall calculation
	Value  float64 // Normalized value (0.0 - 1.0)
}

// ComputeRiskScore calculates risk based on multiple factors:
// - Visibility (public = higher risk)
// - Number of direct callers
// - Number of modules affected
// - Presence of test coverage (future v2)
func ComputeRiskScore(symbol *Symbol, impact []ImpactItem) *RiskScore {
	factors := make([]RiskFactor, 0)

	// Factor 1: Visibility risk
	visibilityScore := calculateVisibilityRisk(symbol, impact)
	factors = append(factors, RiskFactor{
		Name:   "visibility",
		Weight: 0.3,
		Value:  visibilityScore,
	})

	// Factor 2: Direct caller count
	directCallerScore := calculateDirectCallerRisk(impact)
	factors = append(factors, RiskFactor{
		Name:   "direct-callers",
		Weight: 0.35,
		Value:  directCallerScore,
	})

	// Factor 3: Module spread
	moduleSpreadScore := calculateModuleSpreadRisk(impact)
	factors = append(factors, RiskFactor{
		Name:   "module-spread",
		Weight: 0.25,
		Value:  moduleSpreadScore,
	})

	// Factor 4: Impact kind distribution
	impactKindScore := calculateImpactKindRisk(impact)
	factors = append(factors, RiskFactor{
		Name:   "impact-kind",
		Weight: 0.1,
		Value:  impactKindScore,
	})

	// Calculate weighted score
	totalScore := 0.0
	for _, factor := range factors {
		totalScore += factor.Weight * factor.Value
	}

	// Determine risk level
	level := determineRiskLevel(totalScore)

	// Generate explanation
	explanation := generateExplanation(level, factors, impact)

	return &RiskScore{
		Level:       level,
		Score:       totalScore,
		Factors:     factors,
		Explanation: explanation,
	}
}

// calculateVisibilityRisk determines risk based on symbol visibility
func calculateVisibilityRisk(symbol *Symbol, impact []ImpactItem) float64 {
	// Get the most common visibility from impact items
	publicCount := 0
	internalCount := 0
	privateCount := 0

	for _, item := range impact {
		if item.Visibility == nil {
			continue
		}
		switch item.Visibility.Visibility {
		case VisibilityPublic:
			publicCount++
		case VisibilityInternal:
			internalCount++
		case VisibilityPrivate:
			privateCount++
		}
	}

	// If symbol is public or has public references, higher risk
	hasPublic := publicCount > 0
	if hasPublic {
		return 0.9
	}

	// Internal visibility = medium risk
	if internalCount > privateCount {
		return 0.5
	}

	// Private visibility = low risk
	return 0.2
}

// calculateDirectCallerRisk determines risk based on number of direct callers
func calculateDirectCallerRisk(impact []ImpactItem) float64 {
	directCallers, _ := countCallers(impact)

	// Logarithmic scale for caller count
	// 0 callers = 0.0, 1 caller = 0.3, 5 callers = 0.6, 20+ callers = 1.0
	if directCallers == 0 {
		return 0.0
	}

	score := math.Log10(float64(directCallers)+1) / math.Log10(21)
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// calculateModuleSpreadRisk determines risk based on number of affected modules
func calculateModuleSpreadRisk(impact []ImpactItem) float64 {
	moduleSet := make(map[string]bool)
	for _, item := range impact {
		if item.ModuleId != "" {
			moduleSet[item.ModuleId] = true
		}
	}

	moduleCount := len(moduleSet)

	// Logarithmic scale for module count
	// 0-1 modules = 0.2, 2-3 modules = 0.5, 5+ modules = 0.8, 10+ modules = 1.0
	if moduleCount == 0 {
		return 0.0
	}
	if moduleCount == 1 {
		return 0.2
	}

	score := math.Log10(float64(moduleCount)) / math.Log10(10)
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// calculateImpactKindRisk determines risk based on types of impacts
func calculateImpactKindRisk(impact []ImpactItem) float64 {
	hasBreaking := false
	hasImplements := false

	for _, item := range impact {
		if item.Kind == ImplementsInterface {
			hasImplements = true
		}
		if item.Kind == DirectCaller || item.Kind == ImplementsInterface {
			hasBreaking = true
		}
	}

	// Interface implementations are high risk
	if hasImplements {
		return 0.9
	}

	// Direct callers are medium-high risk
	if hasBreaking {
		return 0.7
	}

	// Only type dependencies = medium risk
	return 0.4
}

// determineRiskLevel converts numeric score to risk level
func determineRiskLevel(score float64) RiskLevel {
	if score >= 0.7 {
		return RiskHigh
	}
	if score >= 0.4 {
		return RiskMedium
	}
	return RiskLow
}

// generateExplanation creates a human-readable explanation
func generateExplanation(level RiskLevel, factors []RiskFactor, impact []ImpactItem) string {
	modules := make(map[string]bool)
	for _, item := range impact {
		if item.ModuleId != "" {
			modules[item.ModuleId] = true
		}
	}
	moduleCount := len(modules)

	directCallers, allCallers := countCallers(impact)
	callers := fmt.Sprintf("%d direct caller(s)", directCallers)
	if allCallers > directCallers {
		callers = fmt.Sprintf("%d direct caller(s), %d with transitive ones,", directCallers, allCallers)
	}

	switch level {
	case RiskHigh:
		return fmt.Sprintf("High risk: %s across %d module(s). Changes may break multiple components.", callers, moduleCount)
	case RiskMedium:
		return fmt.Sprintf("Medium risk: %s across %d module(s). Changes require careful testing.", callers, moduleCount)
	case RiskLow:
		return fmt.Sprintf("Low risk: %s across %d module(s). Changes have limited impact.", callers, moduleCount)
	default:
		return "Unknown risk level."
	}
}

// countCallers counts distinct callers: direct ones (distance 1) and those at any
// depth. A function calling twice is one caller; a caller without a symbol (code at
// module level) is told apart by its file and line, and one with neither counts on
// its own.
func countCallers(impact []ImpactItem) (direct, all int) {
	seenDirect := make(map[string]bool)
	seenAll := make(map[string]bool)
	for _, item := range impact {
		if item.Kind != DirectCaller && item.Kind != TransitiveCaller {
			continue
		}
		key := callerKey(item)
		if key == "" || !seenAll[key] {
			seenAll[key] = true
			all++
		}
		if item.Kind == DirectCaller && item.Distance == 1 && (key == "" || !seenDirect[key]) {
			seenDirect[key] = true
			direct++
		}
	}
	return direct, all
}

func callerKey(item ImpactItem) string {
	if item.StableId != "" {
		return item.StableId
	}
	if item.Location != nil && item.Location.FileId != "" {
		return fmt.Sprintf("%s:%d", item.Location.FileId, item.Location.StartLine)
	}
	return ""
}
