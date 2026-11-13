package impact

import (
	"testing"
)

func TestComputeRiskScore(t *testing.T) {
	tests := []struct {
		name          string
		symbol        *Symbol
		impact        []ImpactItem
		expectedLevel RiskLevel
		minScore      float64
		maxScore      float64
	}{
		{
			name: "high risk - many public callers",
			symbol: &Symbol{
				Name:      "publicFunction",
				ModuleId:  "module1",
				Modifiers: []string{"public"},
			},
			impact: []ImpactItem{
				{
					Kind:     DirectCaller,
					Distance: 1,
					ModuleId: "module2",
					Visibility: &VisibilityInfo{
						Visibility: VisibilityPublic,
					},
				},
				{
					Kind:     DirectCaller,
					Distance: 1,
					ModuleId: "module3",
					Visibility: &VisibilityInfo{
						Visibility: VisibilityPublic,
					},
				},
				{
					Kind:     DirectCaller,
					Distance: 1,
					ModuleId: "module4",
					Visibility: &VisibilityInfo{
						Visibility: VisibilityPublic,
					},
				},
				{
					Kind:     DirectCaller,
					Distance: 1,
					ModuleId: "module5",
					Visibility: &VisibilityInfo{
						Visibility: VisibilityPublic,
					},
				},
				{
					Kind:     DirectCaller,
					Distance: 1,
					ModuleId: "module6",
					Visibility: &VisibilityInfo{
						Visibility: VisibilityPublic,
					},
				},
			},
			expectedLevel: RiskHigh,
			minScore:      0.7,
			maxScore:      1.0,
		},
		{
			name: "medium risk - few callers",
			symbol: &Symbol{
				Name:      "internalFunction",
				ModuleId:  "module1",
				Modifiers: []string{"internal"},
			},
			impact: []ImpactItem{
				{
					Kind:     DirectCaller,
					Distance: 1,
					ModuleId: "module1",
					Visibility: &VisibilityInfo{
						Visibility: VisibilityInternal,
					},
				},
				{
					Kind:     DirectCaller,
					Distance: 1,
					ModuleId: "module2",
					Visibility: &VisibilityInfo{
						Visibility: VisibilityInternal,
					},
				},
			},
			expectedLevel: RiskMedium,
			minScore:      0.4,
			maxScore:      0.69,
		},
		{
			name: "low risk - no callers",
			symbol: &Symbol{
				Name:     "unusedFunction",
				ModuleId: "module1",
			},
			impact:        []ImpactItem{},
			expectedLevel: RiskLow,
			minScore:      0.0,
			maxScore:      0.4,
		},
		{
			name: "low risk - only type dependencies",
			symbol: &Symbol{
				Name:     "typeFunction",
				ModuleId: "module1",
			},
			impact: []ImpactItem{
				{
					Kind:     TypeDependency,
					Distance: 1,
					ModuleId: "module2",
					Visibility: &VisibilityInfo{
						Visibility: VisibilityPrivate,
					},
				},
			},
			expectedLevel: RiskLow,
			minScore:      0.0,
			maxScore:      0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComputeRiskScore(tt.symbol, tt.impact)

			if result.Level != tt.expectedLevel {
				t.Errorf("expected level %s, got %s", tt.expectedLevel, result.Level)
			}

			if result.Score < tt.minScore || result.Score > tt.maxScore {
				t.Errorf("score %f out of expected range [%f, %f]", result.Score, tt.minScore, tt.maxScore)
			}

			if len(result.Factors) == 0 {
				t.Error("expected risk factors, got none")
			}

			if result.Explanation == "" {
				t.Error("expected explanation, got empty string")
			}
		})
	}
}

func TestCalculateVisibilityRisk(t *testing.T) {
	tests := []struct {
		name     string
		impact   []ImpactItem
		minScore float64
		maxScore float64
	}{
		{
			name: "high risk - public visibility",
			impact: []ImpactItem{
				{
					Visibility: &VisibilityInfo{
						Visibility: VisibilityPublic,
					},
				},
			},
			minScore: 0.8,
			maxScore: 1.0,
		},
		{
			name: "medium risk - internal visibility",
			impact: []ImpactItem{
				{
					Visibility: &VisibilityInfo{
						Visibility: VisibilityInternal,
					},
				},
				{
					Visibility: &VisibilityInfo{
						Visibility: VisibilityInternal,
					},
				},
			},
			minScore: 0.4,
			maxScore: 0.6,
		},
		{
			name: "low risk - private visibility",
			impact: []ImpactItem{
				{
					Visibility: &VisibilityInfo{
						Visibility: VisibilityPrivate,
					},
				},
			},
			minScore: 0.0,
			maxScore: 0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			symbol := &Symbol{}
			score := calculateVisibilityRisk(symbol, tt.impact)

			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("score %f out of expected range [%f, %f]", score, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestCalculateDirectCallerRisk(t *testing.T) {
	tests := []struct {
		name          string
		directCallers int
		minScore      float64
		maxScore      float64
	}{
		{"no callers", 0, 0.0, 0.0},
		{"one caller", 1, 0.2, 0.4},
		{"five callers", 5, 0.5, 0.7},
		{"many callers", 20, 0.9, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			impact := make([]ImpactItem, tt.directCallers)
			for i := 0; i < tt.directCallers; i++ {
				impact[i] = ImpactItem{
					Kind:     DirectCaller,
					Distance: 1,
				}
			}

			score := calculateDirectCallerRisk(impact)

			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("score %f out of expected range [%f, %f]", score, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestCalculateModuleSpreadRisk(t *testing.T) {
	tests := []struct {
		name        string
		moduleCount int
		minScore    float64
		maxScore    float64
	}{
		{"no modules", 0, 0.0, 0.0},
		{"one module", 1, 0.15, 0.25},
		{"three modules", 3, 0.4, 0.6},
		{"ten modules", 10, 0.9, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			impact := make([]ImpactItem, tt.moduleCount)
			for i := 0; i < tt.moduleCount; i++ {
				impact[i] = ImpactItem{
					ModuleId: string(rune('A' + i)),
				}
			}

			score := calculateModuleSpreadRisk(impact)

			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("score %f out of expected range [%f, %f]", score, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestCalculateImpactKindRisk(t *testing.T) {
	tests := []struct {
		name     string
		impact   []ImpactItem
		minScore float64
		maxScore float64
	}{
		{
			name: "interface implementation - high risk",
			impact: []ImpactItem{
				{Kind: ImplementsInterface},
			},
			minScore: 0.85,
			maxScore: 0.95,
		},
		{
			name: "direct callers - medium-high risk",
			impact: []ImpactItem{
				{Kind: DirectCaller},
			},
			minScore: 0.65,
			maxScore: 0.75,
		},
		{
			name: "type dependencies only - medium risk",
			impact: []ImpactItem{
				{Kind: TypeDependency},
			},
			minScore: 0.35,
			maxScore: 0.45,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateImpactKindRisk(tt.impact)

			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("score %f out of expected range [%f, %f]", score, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestDetermineRiskLevel(t *testing.T) {
	tests := []struct {
		score    float64
		expected RiskLevel
	}{
		{0.0, RiskLow},
		{0.3, RiskLow},
		{0.39, RiskLow},
		{0.4, RiskMedium},
		{0.5, RiskMedium},
		{0.69, RiskMedium},
		{0.7, RiskHigh},
		{0.8, RiskHigh},
		{1.0, RiskHigh},
	}

	for _, tt := range tests {
		t.Run("score "+string(rune(tt.score*100)), func(t *testing.T) {
			result := determineRiskLevel(tt.score)
			if result != tt.expected {
				t.Errorf("score %f: expected %s, got %s", tt.score, tt.expected, result)
			}
		})
	}
}

func TestGenerateExplanation(t *testing.T) {
	tests := []struct {
		name   string
		level  RiskLevel
		impact []ImpactItem
	}{
		{
			name:  "high risk explanation",
			level: RiskHigh,
			impact: []ImpactItem{
				{Kind: DirectCaller, Distance: 1, ModuleId: "m1"},
				{Kind: DirectCaller, Distance: 1, ModuleId: "m2"},
			},
		},
		{
			name:  "medium risk explanation",
			level: RiskMedium,
			impact: []ImpactItem{
				{Kind: DirectCaller, Distance: 1, ModuleId: "m1"},
			},
		},
		{
			name:   "low risk explanation",
			level:  RiskLow,
			impact: []ImpactItem{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			explanation := generateExplanation(tt.level, []RiskFactor{}, tt.impact)

			if explanation == "" {
				t.Error("expected non-empty explanation")
			}

			// Check that the explanation contains the risk level
			switch tt.level {
			case RiskHigh:
				if len(explanation) < 10 {
					t.Error("high risk explanation too short")
				}
			case RiskMedium:
				if len(explanation) < 10 {
					t.Error("medium risk explanation too short")
				}
			case RiskLow:
				if len(explanation) < 10 {
					t.Error("low risk explanation too short")
				}
			}
		})
	}
}

// A function calling twice is one caller, and the explanation names the transitive
// ones the blast radius counts too.
func TestCountCallers_Distinct(t *testing.T) {
	loc := func(line int) *Location { return &Location{FileId: "a.py", StartLine: line} }
	impact := []ImpactItem{
		{Kind: DirectCaller, Distance: 1, StableId: "bill", Location: loc(415)},
		{Kind: DirectCaller, Distance: 1, StableId: "bill", Location: loc(424)},
		{Kind: DirectCaller, Distance: 1, StableId: "simulate", Location: loc(352)},
		{Kind: DirectCaller, Distance: 1, Location: loc(10)}, // module level
		{Kind: TransitiveCaller, Distance: 2, StableId: "run", Location: loc(487)},
	}
	if direct, all := countCallers(impact); direct != 3 || all != 4 {
		t.Errorf("countCallers = %d, %d; want 3, 4", direct, all)
	}
	if got := RecomputeBlastRadius(impact).UniqueCallerCount; got != 4 {
		t.Errorf("UniqueCallerCount = %d, want 4", got)
	}
	if got := generateExplanation(RiskMedium, nil, impact); got != "Medium risk: 3 direct caller(s), 4 with transitive ones, across 0 module(s). Changes require careful testing." {
		t.Errorf("explanation = %q", got)
	}
}
