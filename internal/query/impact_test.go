package query

import (
	"math"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
	"github.com/SimplyLiz/CodeMCP/internal/telemetry"
)

func ptrF64(v float64) *float64 { return &v }

func TestBridgeMultiplierFromGraph(t *testing.T) {
	tests := []struct {
		name       string
		nodes      []cartographer.GraphNode
		files      []string
		wantMul    float64
		wantFactor bool
		wantValue  float64 // only checked when wantFactor=true
	}{
		{
			name:       "no nodes",
			nodes:      nil,
			files:      []string{"a.go"},
			wantMul:    1.0,
			wantFactor: false,
		},
		{
			name:       "no files",
			nodes:      []cartographer.GraphNode{{Path: "a.go", BridgeScore: ptrF64(500)}},
			files:      nil,
			wantMul:    1.0,
			wantFactor: false,
		},
		{
			name: "no BridgeScore populated",
			nodes: []cartographer.GraphNode{
				{Path: "a.go", BridgeScore: nil},
			},
			files:      []string{"a.go"},
			wantMul:    1.0,
			wantFactor: false,
		},
		{
			name: "file does not match any node",
			nodes: []cartographer.GraphNode{
				{Path: "b.go", BridgeScore: ptrF64(500)},
			},
			files:      []string{"a.go"},
			wantMul:    1.0,
			wantFactor: false,
		},
		{
			name: "match by path yields 1 + score/1000",
			nodes: []cartographer.GraphNode{
				{Path: "a.go", BridgeScore: ptrF64(250)},
			},
			files:      []string{"a.go"},
			wantMul:    1.25,
			wantFactor: true,
			wantValue:  0.25,
		},
		{
			name: "match by module id",
			nodes: []cartographer.GraphNode{
				{ModuleID: "pkg/foo", Path: "", BridgeScore: ptrF64(600)},
			},
			files:      []string{"pkg/foo"},
			wantMul:    1.6,
			wantFactor: true,
			wantValue:  0.6,
		},
		{
			name: "multiple files takes max score",
			nodes: []cartographer.GraphNode{
				{Path: "low.go", BridgeScore: ptrF64(100)},
				{Path: "high.go", BridgeScore: ptrF64(800)},
				{Path: "mid.go", BridgeScore: ptrF64(400)},
			},
			files:      []string{"low.go", "high.go", "mid.go"},
			wantMul:    1.8,
			wantFactor: true,
			wantValue:  0.8,
		},
		{
			name: "cap at 2.0 for over-1000 scores",
			nodes: []cartographer.GraphNode{
				{Path: "a.go", BridgeScore: ptrF64(1500)},
			},
			files:      []string{"a.go"},
			wantMul:    2.0,
			wantFactor: true,
			wantValue:  1.5,
		},
		{
			name: "path match wins over module match for same file",
			nodes: []cartographer.GraphNode{
				{Path: "a.go", ModuleID: "a.go", BridgeScore: ptrF64(300)},
				{Path: "", ModuleID: "a.go", BridgeScore: ptrF64(900)},
			},
			files:      []string{"a.go"},
			wantMul:    1.3,
			wantFactor: true,
			wantValue:  0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mul, factor := bridgeMultiplierFromGraph(tt.nodes, tt.files)
			if math.Abs(mul-tt.wantMul) > 1e-9 {
				t.Errorf("multiplier = %v, want %v", mul, tt.wantMul)
			}
			if tt.wantFactor {
				if factor == nil {
					t.Fatalf("expected factor, got nil")
				}
				if factor.Name != "bridge_centrality" {
					t.Errorf("factor.Name = %q, want %q", factor.Name, "bridge_centrality")
				}
				if factor.Weight != 0 {
					t.Errorf("factor.Weight = %v, want 0 (informational)", factor.Weight)
				}
				if math.Abs(factor.Value-tt.wantValue) > 1e-9 {
					t.Errorf("factor.Value = %v, want %v", factor.Value, tt.wantValue)
				}
			} else if factor != nil {
				t.Errorf("expected nil factor, got %+v", factor)
			}
		})
	}
}

func TestFilterTestReferences(t *testing.T) {
	tests := []struct {
		name     string
		refs     []impact.Reference
		expected int
	}{
		{
			name:     "empty slice",
			refs:     []impact.Reference{},
			expected: 0,
		},
		{
			name: "all non-test refs",
			refs: []impact.Reference{
				{Location: &impact.Location{FileId: "foo.go"}, IsTest: false},
				{Location: &impact.Location{FileId: "bar.go"}, IsTest: false},
			},
			expected: 2,
		},
		{
			name: "all test refs",
			refs: []impact.Reference{
				{Location: &impact.Location{FileId: "foo_test.go"}, IsTest: true},
				{Location: &impact.Location{FileId: "bar_test.go"}, IsTest: true},
			},
			expected: 0,
		},
		{
			name: "mixed refs",
			refs: []impact.Reference{
				{Location: &impact.Location{FileId: "foo.go"}, IsTest: false},
				{Location: &impact.Location{FileId: "foo_test.go"}, IsTest: true},
				{Location: &impact.Location{FileId: "bar.go"}, IsTest: false},
				{Location: &impact.Location{FileId: "bar_test.go"}, IsTest: true},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := splitTestReferences(tt.refs)
			if len(result) != tt.expected {
				t.Errorf("got %d refs, want %d", len(result), tt.expected)
			}
			for _, ref := range result {
				if ref.IsTest {
					t.Error("result contains test reference")
				}
			}
		})
	}
}

func TestConvertImpactItems(t *testing.T) {
	tests := []struct {
		name  string
		items []impact.ImpactItem
		want  int
	}{
		{
			name:  "empty slice",
			items: []impact.ImpactItem{},
			want:  0,
		},
		{
			name: "single item without location",
			items: []impact.ImpactItem{
				{
					StableId:   "sym1",
					Name:       "Foo",
					Kind:       "direct-caller",
					Distance:   1,
					ModuleId:   "mod1",
					Confidence: 0.9,
				},
			},
			want: 1,
		},
		{
			name: "item with location and visibility",
			items: []impact.ImpactItem{
				{
					StableId:   "sym1",
					Name:       "Foo",
					Kind:       "direct-caller",
					Distance:   1,
					ModuleId:   "mod1",
					Confidence: 0.9,
					Location: &impact.Location{
						FileId:      "foo.go",
						StartLine:   10,
						StartColumn: 5,
					},
					Visibility: &impact.VisibilityInfo{
						Visibility: "public",
						Confidence: 0.8,
						Source:     "scip",
					},
				},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertImpactItems(tt.items)
			if len(result) != tt.want {
				t.Errorf("got %d items, want %d", len(result), tt.want)
			}
			if len(tt.items) > 0 && len(result) > 0 {
				if result[0].StableId != tt.items[0].StableId {
					t.Errorf("StableId: got %q, want %q", result[0].StableId, tt.items[0].StableId)
				}
				if result[0].Kind != string(tt.items[0].Kind) {
					t.Errorf("Kind: got %q, want %q", result[0].Kind, tt.items[0].Kind)
				}
			}
		})
	}
}

func TestConvertModuleImpacts(t *testing.T) {
	modules := []impact.ModuleSummary{
		{ModuleId: "mod1", Name: "Module One", ImpactCount: 5},
		{ModuleId: "mod2", Name: "Module Two", ImpactCount: 10},
	}

	result := convertModuleImpacts(modules)

	if len(result) != 2 {
		t.Fatalf("got %d modules, want 2", len(result))
	}
	if result[0].ModuleId != "mod1" {
		t.Errorf("ModuleId[0]: got %q, want %q", result[0].ModuleId, "mod1")
	}
	if result[1].ImpactCount != 10 {
		t.Errorf("ImpactCount[1]: got %d, want %d", result[1].ImpactCount, 10)
	}
}

func TestConvertRiskScore(t *testing.T) {
	tests := []struct {
		name     string
		input    *impact.RiskScore
		wantNil  bool
		wantLvl  string
		wantExpl string
	}{
		{
			name:     "nil input",
			input:    nil,
			wantLvl:  "unknown",
			wantExpl: "Unable to compute risk score",
		},
		{
			name: "valid risk score",
			input: &impact.RiskScore{
				Level:       "high",
				Score:       0.75,
				Explanation: "High impact change",
				Factors: []impact.RiskFactor{
					{Name: "visibility", Value: 0.8, Weight: 0.3},
				},
			},
			wantLvl:  "high",
			wantExpl: "High impact change",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertRiskScore(tt.input)
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.Level != tt.wantLvl {
				t.Errorf("Level: got %q, want %q", result.Level, tt.wantLvl)
			}
			if result.Explanation != tt.wantExpl {
				t.Errorf("Explanation: got %q, want %q", result.Explanation, tt.wantExpl)
			}
		})
	}
}

func TestSortImpactItems(t *testing.T) {
	items := []ImpactItem{
		{StableId: "s1", Kind: "transitive-caller", Confidence: 0.9},
		{StableId: "s2", Kind: "direct-caller", Confidence: 0.7},
		{StableId: "s3", Kind: "direct-caller", Confidence: 0.9},
		{StableId: "s4", Kind: "test-dependency", Confidence: 1.0},
		{StableId: "s5", Kind: "type-dependency", Confidence: 0.5},
	}

	sortImpactItems(items)

	// Expected order: direct-caller first, then by confidence desc
	expectedOrder := []string{"s3", "s2", "s1", "s5", "s4"}
	for i, item := range items {
		if item.StableId != expectedOrder[i] {
			t.Errorf("position %d: got %q, want %q", i, item.StableId, expectedOrder[i])
		}
	}
}

func TestComputeTelemetryPeriodFilter(t *testing.T) {
	tests := []struct {
		period   string
		expected string // Just check format, not exact date
	}{
		{"7d", "20"},      // Should start with year prefix
		{"30d", "20"},     // Should start with year prefix
		{"90d", "20"},     // Should start with year prefix
		{"all", ""},       // Empty for all
		{"invalid", "20"}, // Falls back to 90d
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			result := computeTelemetryPeriodFilter(tt.period)
			if tt.expected == "" {
				if result != "" {
					t.Errorf("got %q, want empty", result)
				}
			} else {
				if len(result) == 0 || result[:2] != tt.expected {
					t.Errorf("got %q, want prefix %q", result, tt.expected)
				}
			}
		})
	}
}

func TestComputeUsageTrend(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		usages []telemetry.ObservedUsage
		want   string
	}{
		{
			name:   "empty",
			usages: []telemetry.ObservedUsage{},
			want:   "stable",
		},
		{
			name: "single entry",
			usages: []telemetry.ObservedUsage{
				{CallCount: 100, IngestedAt: now},
			},
			want: "stable",
		},
		{
			name: "increasing trend",
			usages: []telemetry.ObservedUsage{
				{CallCount: 200, IngestedAt: now},                      // Recent (high)
				{CallCount: 100, IngestedAt: now.Add(-24 * time.Hour)}, // Older (low)
			},
			want: "increasing",
		},
		{
			name: "decreasing trend",
			usages: []telemetry.ObservedUsage{
				{CallCount: 50, IngestedAt: now},                       // Recent (low)
				{CallCount: 200, IngestedAt: now.Add(-24 * time.Hour)}, // Older (high)
			},
			want: "decreasing",
		},
		{
			name: "stable trend",
			usages: []telemetry.ObservedUsage{
				{CallCount: 100, IngestedAt: now},
				{CallCount: 100, IngestedAt: now.Add(-24 * time.Hour)},
			},
			want: "stable",
		},
		{
			name: "increasing from zero older",
			usages: []telemetry.ObservedUsage{
				{CallCount: 100, IngestedAt: now},
				{CallCount: 0, IngestedAt: now.Add(-24 * time.Hour)},
			},
			want: "increasing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeUsageTrend(tt.usages)
			if result != tt.want {
				t.Errorf("got %q, want %q", result, tt.want)
			}
		})
	}
}

func TestComputeBlendedConfidenceScore(t *testing.T) {
	tests := []struct {
		name     string
		static   float64
		observed float64
		wantMin  float64
		wantMax  float64
	}{
		{
			name:     "static higher",
			static:   0.9,
			observed: 0.5,
			wantMin:  0.9,
			wantMax:  0.93, // with small agreement boost
		},
		{
			name:     "observed higher",
			static:   0.5,
			observed: 0.9,
			wantMin:  0.9,
			wantMax:  0.93,
		},
		{
			name:     "both high - agreement boost",
			static:   0.8,
			observed: 0.8,
			wantMin:  0.83,
			wantMax:  0.84,
		},
		{
			name:     "both low - no boost",
			static:   0.3,
			observed: 0.4,
			wantMin:  0.4,
			wantMax:  0.4,
		},
		{
			name:     "cap at 1.0",
			static:   0.99,
			observed: 0.99,
			wantMin:  1.0,
			wantMax:  1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeBlendedConfidenceScore(tt.static, tt.observed)
			if result < tt.wantMin || result > tt.wantMax {
				t.Errorf("got %.3f, want [%.3f, %.3f]", result, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestIsTestFilePathEnhanced(t *testing.T) {
	tests := []struct {
		path   string
		isTest bool
	}{
		// Go tests
		{"foo_test.go", true},
		{"internal/query/engine_test.go", true},
		{"foo.go", false},

		// TypeScript/JavaScript tests
		{"foo.test.ts", true},
		{"foo.spec.ts", true},
		{"foo.test.js", true},
		{"foo.spec.js", true},
		{"foo.test.tsx", true},
		{"foo.spec.tsx", true},
		{"foo.ts", false},
		{"foo.js", false},

		// Python tests
		{"test_foo.py", true},
		{"foo_test.py", true},
		{"foo.py", false},

		// Dart tests
		{"foo_test.dart", true},
		{"foo.dart", false},

		// Directory patterns (need leading /)
		{"some/test/foo.go", true},
		{"some/tests/foo.py", true},
		{"some/__tests__/foo.ts", true},
		{"some/spec/foo.rb", true},
		{"src/foo.go", false},

		// Case insensitive
		{"FOO_TEST.GO", true},
		{"Foo.Test.Ts", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isTestFilePath(tt.path)
			if result != tt.isTest {
				t.Errorf("isTestFilePath(%q) = %v, want %v", tt.path, result, tt.isTest)
			}
		})
	}
}

func TestCategorizeTestReason(t *testing.T) {
	tests := []struct {
		distance int
		want     string
	}{
		{0, "direct"},
		{1, "direct"},
		{2, "transitive"},
		{3, "transitive"},
		{10, "transitive"},
	}

	for _, tt := range tests {
		result := categorizeTestReason(tt.distance)
		if result != tt.want {
			t.Errorf("categorizeTestReason(%d) = %q, want %q", tt.distance, result, tt.want)
		}
	}
}

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		name     string
		loc      *LocationInfo
		expected bool
	}{
		{
			name:     "nil location",
			loc:      nil,
			expected: false,
		},
		{
			name:     "test file",
			loc:      &LocationInfo{FileId: "foo_test.go"},
			expected: true,
		},
		{
			name:     "non-test file",
			loc:      &LocationInfo{FileId: "foo.go"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTestFile(tt.loc)
			if result != tt.expected {
				t.Errorf("isTestFile() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMin(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 10, 0},
		{-5, 5, -5},
	}

	for _, tt := range tests {
		result := min(tt.a, tt.b)
		if result != tt.want {
			t.Errorf("min(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.want)
		}
	}
}

func TestFileExists(t *testing.T) {
	// Test with a file that definitely exists
	if !fileExists("/etc/hosts") {
		// Skip on systems without /etc/hosts
		t.Skip("skipping test, /etc/hosts not found")
	}

	// Test with a file that doesn't exist
	if fileExists("/nonexistent/file/path/12345") {
		t.Error("fileExists returned true for nonexistent file")
	}
}
