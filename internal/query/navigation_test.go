package query

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
)

func TestComputeJustifyVerdict(t *testing.T) {
	t.Run("keeps symbol with active callers", func(t *testing.T) {
		facts := ExplainSymbolFacts{
			Usage: &ExplainUsage{CallerCount: 5},
		}
		verdict, confidence, reasoning := computeJustifyVerdict(facts)

		if verdict != "keep" {
			t.Errorf("expected verdict 'keep', got %q", verdict)
		}
		if confidence != 0.9 {
			t.Errorf("expected confidence 0.9, got %f", confidence)
		}
		if reasoning == "" {
			t.Error("expected non-empty reasoning")
		}
	})

	t.Run("investigates public API with no callers", func(t *testing.T) {
		facts := ExplainSymbolFacts{
			Usage: &ExplainUsage{CallerCount: 0},
			Flags: &ExplainSymbolFlags{IsPublicApi: true},
		}
		verdict, confidence, reasoning := computeJustifyVerdict(facts)

		if verdict != "investigate" {
			t.Errorf("expected verdict 'investigate', got %q", verdict)
		}
		if confidence != 0.6 {
			t.Errorf("expected confidence 0.6, got %f", confidence)
		}
		if reasoning == "" {
			t.Error("expected non-empty reasoning")
		}
	})

	t.Run("removes private symbol with no callers", func(t *testing.T) {
		facts := ExplainSymbolFacts{
			Usage: &ExplainUsage{CallerCount: 0},
			Flags: &ExplainSymbolFlags{IsPublicApi: false},
		}
		verdict, confidence, reasoning := computeJustifyVerdict(facts)

		if verdict != "remove-candidate" {
			t.Errorf("expected verdict 'remove-candidate', got %q", verdict)
		}
		if confidence != 0.7 {
			t.Errorf("expected confidence 0.7, got %f", confidence)
		}
		if reasoning == "" {
			t.Error("expected non-empty reasoning")
		}
	})

	t.Run("removes when no usage info available", func(t *testing.T) {
		facts := ExplainSymbolFacts{}
		verdict, confidence, reasoning := computeJustifyVerdict(facts)

		if verdict != "remove-candidate" {
			t.Errorf("expected verdict 'remove-candidate', got %q", verdict)
		}
		if confidence != 0.7 {
			t.Errorf("expected confidence 0.7, got %f", confidence)
		}
		if reasoning == "" {
			t.Error("expected non-empty reasoning")
		}
	})
}

func TestClassifyCommitFrequency(t *testing.T) {
	tests := []struct {
		count    int
		expected string
	}{
		{0, "unknown"},
		{1, "stable"},
		{10, "stable"},
		{11, "moderate"},
		{50, "moderate"},
		{51, "volatile"},
		{100, "volatile"},
	}

	for _, tc := range tests {
		result := classifyCommitFrequency(tc.count)
		if result != tc.expected {
			t.Errorf("classifyCommitFrequency(%d) = %q, expected %q", tc.count, result, tc.expected)
		}
	}
}

func TestTopLevelModule(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"internal/query/engine.go", "internal"},
		{"./internal/query/engine.go", "internal"},
		{"cmd/ckb/main.go", "cmd"},
		{"main.go", "main.go"},
		{"", ""},
	}

	for _, tc := range tests {
		result := topLevelModule(tc.path)
		if result != tc.expected {
			t.Errorf("topLevelModule(%q) = %q, expected %q", tc.path, result, tc.expected)
		}
	}
}

func TestBuildExplainSummary(t *testing.T) {
	t.Run("builds complete summary", func(t *testing.T) {
		facts := ExplainSymbolFacts{
			Symbol: &SymbolInfo{
				Name:     "MyFunction",
				Kind:     "function",
				ModuleId: "internal/query",
			},
			Usage: &ExplainUsage{
				CallerCount:    5,
				ReferenceCount: 10,
				ModuleCount:    3,
			},
			History: &ExplainHistory{
				CommitCount:    15,
				LastModifiedAt: "2024-01-15",
			},
		}

		summary := buildExplainSummary(facts)

		if summary.Identity == "" {
			t.Error("expected non-empty identity")
		}
		if summary.Usage == "" {
			t.Error("expected non-empty usage")
		}
		if summary.History == "" {
			t.Error("expected non-empty history")
		}
		if summary.Tldr == "" {
			t.Error("expected non-empty tldr")
		}
	})

	t.Run("handles empty facts", func(t *testing.T) {
		facts := ExplainSymbolFacts{}
		summary := buildExplainSummary(facts)

		if summary.Tldr != "" {
			t.Errorf("expected empty tldr for empty facts, got %q", summary.Tldr)
		}
	})
}

// =============================================================================
// Phase 2: summarizeDiff Tests
// =============================================================================

func TestClassifyFileRiskLevel(t *testing.T) {
	tests := []struct {
		name     string
		stat     git.DiffStats
		role     string
		expected string
	}{
		{
			name:     "deleted file is high risk",
			stat:     git.DiffStats{FilePath: "foo.go", IsDeleted: true},
			role:     "core",
			expected: "high",
		},
		{
			name:     "new file is low risk",
			stat:     git.DiffStats{FilePath: "foo.go", IsNew: true},
			role:     "core",
			expected: "low",
		},
		{
			name:     "core file with large changes is high risk",
			stat:     git.DiffStats{FilePath: "foo.go", Additions: 80, Deletions: 30},
			role:     "core",
			expected: "high",
		},
		{
			name:     "core file with medium changes is medium risk",
			stat:     git.DiffStats{FilePath: "foo.go", Additions: 20, Deletions: 15},
			role:     "core",
			expected: "medium",
		},
		{
			name:     "config change is medium risk",
			stat:     git.DiffStats{FilePath: "config.json", Additions: 5, Deletions: 2},
			role:     "config",
			expected: "medium",
		},
		{
			name:     "test change is low risk",
			stat:     git.DiffStats{FilePath: "foo_test.go", Additions: 50, Deletions: 20},
			role:     "test",
			expected: "low",
		},
		{
			name:     "very large change is high risk",
			stat:     git.DiffStats{FilePath: "foo.go", Additions: 150, Deletions: 100},
			role:     "unknown",
			expected: "high",
		},
		{
			name:     "small change is low risk",
			stat:     git.DiffStats{FilePath: "foo.go", Additions: 10, Deletions: 5},
			role:     "unknown",
			expected: "low",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := classifyFileRiskLevel(tc.stat, tc.role)
			if result != tc.expected {
				t.Errorf("classifyFileRiskLevel(%+v, %q) = %q, expected %q",
					tc.stat, tc.role, result, tc.expected)
			}
		})
	}
}

func TestSuggestTestPath(t *testing.T) {
	tests := []struct {
		filePath string
		language string
		expected string
	}{
		{"internal/query/engine.go", "go", "internal/query/engine_test.go"},
		{"src/utils.ts", "typescript", "src/utils.test.ts"},
		{"src/utils.js", "javascript", "src/utils.test.js"},
		{"lib/helper.py", "python", "lib/test_helper.py"},
		{"foo.rs", "rust", ""},
	}

	for _, tc := range tests {
		t.Run(tc.filePath, func(t *testing.T) {
			result := suggestTestPath(tc.filePath, tc.language)
			if result != tc.expected {
				t.Errorf("suggestTestPath(%q, %q) = %q, expected %q",
					tc.filePath, tc.language, result, tc.expected)
			}
		})
	}
}

func TestBuildDiffSummary(t *testing.T) {
	t.Run("builds summary with changes", func(t *testing.T) {
		files := []DiffFileChange{
			{FilePath: "a.go", ChangeType: "added"},
			{FilePath: "b.go", ChangeType: "modified"},
			{FilePath: "c.go", ChangeType: "modified"},
			{FilePath: "d.go", ChangeType: "deleted"},
		}
		commits := []DiffCommitInfo{
			{Hash: "abc123"},
			{Hash: "def456"},
		}

		summary := buildDiffSummary(files, nil, nil, commits)

		if summary.OneLiner == "" {
			t.Error("expected non-empty one-liner")
		}
		if len(summary.KeyChanges) == 0 {
			t.Error("expected key changes")
		}
	})

	t.Run("handles empty files", func(t *testing.T) {
		summary := buildDiffSummary(nil, nil, nil, nil)
		if summary.OneLiner != "0 files changed" {
			t.Errorf("expected '0 files changed', got %q", summary.OneLiner)
		}
	})
}

func TestComputeDiffConfidence(t *testing.T) {
	t.Run("git and scip available", func(t *testing.T) {
		basis := []ConfidenceBasisItem{
			{Backend: "git", Status: "available"},
			{Backend: "scip", Status: "available"},
		}
		conf := computeDiffConfidence(basis, nil)
		if conf != 0.89 {
			t.Errorf("expected 0.89, got %f", conf)
		}
	})

	t.Run("git only", func(t *testing.T) {
		basis := []ConfidenceBasisItem{
			{Backend: "git", Status: "available"},
			{Backend: "scip", Status: "missing"},
		}
		conf := computeDiffConfidence(basis, nil)
		if conf != 0.69 {
			t.Errorf("expected 0.69, got %f", conf)
		}
	})

	t.Run("git unavailable", func(t *testing.T) {
		basis := []ConfidenceBasisItem{
			{Backend: "git", Status: "missing"},
		}
		conf := computeDiffConfidence(basis, nil)
		if conf != 0.39 {
			t.Errorf("expected 0.39, got %f", conf)
		}
	})

	t.Run("with limitations", func(t *testing.T) {
		basis := []ConfidenceBasisItem{
			{Backend: "git", Status: "available"},
			{Backend: "scip", Status: "available"},
		}
		limitations := []string{"truncated"}
		conf := computeDiffConfidence(basis, limitations)
		if conf != 0.79 {
			t.Errorf("expected 0.79, got %f", conf)
		}
	})
}

// =============================================================================
// Phase 3: getHotspots Tests
// =============================================================================

func TestClassifyRecency(t *testing.T) {
	tests := []struct {
		name         string
		lastModified string
		expected     string
	}{
		{"empty timestamp", "", "stale"},
		{"invalid timestamp", "not-a-date", "stale"},
		// Note: actual date tests would need time mocking
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := classifyRecency(tc.lastModified)
			if result != tc.expected {
				t.Errorf("classifyRecency(%q) = %q, expected %q",
					tc.lastModified, result, tc.expected)
			}
		})
	}
}

func TestClassifyHotspotRisk(t *testing.T) {
	tests := []struct {
		name     string
		churn    git.ChurnMetrics
		role     string
		expected string
	}{
		{
			name:     "high churn core file",
			churn:    git.ChurnMetrics{ChangeCount: 25, AuthorCount: 2},
			role:     "core",
			expected: "high",
		},
		{
			name:     "many authors",
			churn:    git.ChurnMetrics{ChangeCount: 5, AuthorCount: 6},
			role:     "unknown",
			expected: "high",
		},
		{
			name:     "very high churn",
			churn:    git.ChurnMetrics{ChangeCount: 35, AuthorCount: 2},
			role:     "unknown",
			expected: "high",
		},
		{
			name:     "moderate churn",
			churn:    git.ChurnMetrics{ChangeCount: 15, AuthorCount: 2},
			role:     "unknown",
			expected: "medium",
		},
		{
			name:     "low churn",
			churn:    git.ChurnMetrics{ChangeCount: 5, AuthorCount: 1},
			role:     "unknown",
			expected: "low",
		},
		{
			name:     "test file with high churn",
			churn:    git.ChurnMetrics{ChangeCount: 20, AuthorCount: 2},
			role:     "test",
			expected: "medium",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := classifyHotspotRisk(tc.churn, tc.role)
			if result != tc.expected {
				t.Errorf("classifyHotspotRisk(%+v, %q) = %q, expected %q",
					tc.churn, tc.role, result, tc.expected)
			}
		})
	}
}

// =============================================================================
// Phase 4: explainPath Tests
// =============================================================================

func TestClassifyPathRole(t *testing.T) {
	tests := []struct {
		path         string
		expectedRole string
	}{
		// Test files (note: /test/ and /tests/ and /__tests__/ need slashes)
		{"internal/query/engine_test.go", "test-only"},
		{"src/utils.test.ts", "test-only"},
		{"app/test/helper.go", "test-only"},
		{"app/__tests__/app.js", "test-only"},

		// Config files (any path containing "config", ".json", etc.)
		{"config.json", "config"},
		{"settings.yaml", "config"},
		{".eslintrc.js", "config"},
		{"tsconfig.json", "config"},

		// Documentation
		{"README.md", "unknown"},
		{"app/docs/guide.txt", "unknown"},

		// Vendor (needs /vendor/ or /node_modules/ with slashes)
		{"app/vendor/github.com/foo/bar.go", "unknown"},
		{"app/node_modules/lodash/index.js", "unknown"},

		// Glue/integration (handler, middleware, routes, etc.)
		{"internal/api/handler.go", "glue"},
		{"src/middleware/auth.ts", "glue"},
		{"routes/api.go", "glue"},

		// Legacy
		{"legacy/old_code.go", "legacy"},
		{"deprecated/helper.js", "legacy"},

		// Entry points (note: /cmd/ needs slash, or main.go suffix, or index.ts/js)
		{"app/cmd/server/main.go", "core"},
		{"src/index.ts", "core"},

		// Core (needs /internal/, /src/, /lib/, /pkg/, /core/, /domain/, /services/)
		{"app/internal/query/engine.go", "core"},
		{"app/src/services/user.ts", "core"},
		{"app/lib/utils.py", "core"},

		// Unknown
		{"random.go", "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			role, _, basis := classifyPathRole(tc.path)
			if role != tc.expectedRole {
				t.Errorf("classifyPathRole(%q) = %q, expected %q", tc.path, role, tc.expectedRole)
			}
			if len(basis) == 0 {
				t.Error("expected non-empty classification basis")
			}
		})
	}
}

func TestComputePathConfidence(t *testing.T) {
	tests := []struct {
		name     string
		basis    []ClassificationBasis
		expected float64
	}{
		{
			name:     "empty basis",
			basis:    nil,
			expected: 0.5,
		},
		{
			name: "high confidence naming",
			basis: []ClassificationBasis{
				{Type: "naming", Signal: "test pattern", Confidence: 0.95},
			},
			expected: 0.79, // Capped at 0.79
		},
		{
			name: "low confidence",
			basis: []ClassificationBasis{
				{Type: "naming", Signal: "no match", Confidence: 0.5},
			},
			expected: 0.5,
		},
		{
			name: "multiple basis items",
			basis: []ClassificationBasis{
				{Type: "naming", Confidence: 0.6},
				{Type: "location", Confidence: 0.75},
			},
			expected: 0.75,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := computePathConfidence(tc.basis)
			if result != tc.expected {
				t.Errorf("computePathConfidence(%+v) = %f, expected %f",
					tc.basis, result, tc.expected)
			}
		})
	}
}

// =============================================================================
// Phase 4: listKeyConcepts Tests
// =============================================================================

func TestExtractConcept(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		// Skip common names
		{"main", ""},
		{"init", ""},
		{"get", ""},
		{"test", ""},

		// Extract from simple names
		{"User", "User"},
		{"Cache", "Cache"},

		// Extract from compound names (skip suffixes)
		{"UserService", "User"},
		{"CacheManager", "Cache"},
		{"AuthHandler", "Auth"},
		{"ConfigProvider", "Config"},

		// Short words are skipped
		{"Do", ""},
		{"AB", ""},

		// All suffixes - use first
		{"ServiceHandler", "Service"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractConcept(tc.name)
			if result != tc.expected {
				t.Errorf("extractConcept(%q) = %q, expected %q", tc.name, result, tc.expected)
			}
		})
	}
}

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"user", []string{"user"}},
		{"User", []string{"User"}},
		{"UserService", []string{"User", "Service"}},
		{"HTTPHandler", []string{"H", "T", "T", "P", "Handler"}},
		{"getUser", []string{"get", "User"}},
		{"XMLParser", []string{"X", "M", "L", "Parser"}},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := splitCamelCase(tc.input)
			if len(result) != len(tc.expected) {
				t.Errorf("splitCamelCase(%q) = %v, expected %v", tc.input, result, tc.expected)
				return
			}
			for i := range result {
				if result[i] != tc.expected[i] {
					t.Errorf("splitCamelCase(%q)[%d] = %q, expected %q",
						tc.input, i, result[i], tc.expected[i])
				}
			}
		})
	}
}

func TestCategorizeConceptV52(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		// Technical
		{"Cache", "technical"},
		{"Queue", "technical"},
		{"Database", "technical"},
		{"HTTP", "technical"},

		// Pattern
		{"Factory", "pattern"},
		{"Builder", "pattern"},
		{"Adapter", "pattern"},
		{"Observer", "pattern"},

		// Domain (default)
		{"User", "domain"},
		{"Order", "domain"},
		{"Payment", "domain"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := categorizeConceptV52(tc.name)
			if result != tc.expected {
				t.Errorf("categorizeConceptV52(%q) = %q, expected %q", tc.name, result, tc.expected)
			}
		})
	}
}

func TestTitleCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"a", "A"},
		{"hello", "Hello"},
		{"HELLO", "HELLO"},
		{"hELLO", "HELLO"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := titleCase(tc.input)
			if result != tc.expected {
				t.Errorf("titleCase(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

// =============================================================================
// Phase 4: recentlyRelevant Tests
// =============================================================================

func TestComputeRecencyScore(t *testing.T) {
	tests := []struct {
		name      string
		timestamp string
		expected  float64
	}{
		{"empty timestamp", "", 0},
		{"invalid timestamp", "not-a-date", 0},
		// Note: actual date tests would need time mocking or very old dates
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := computeRecencyScore(tc.timestamp)
			if result != tc.expected {
				t.Errorf("computeRecencyScore(%q) = %f, expected %f",
					tc.timestamp, result, tc.expected)
			}
		})
	}
}

// =============================================================================
// File Role Classification Tests (shared helper)
// =============================================================================

func TestClassifyFileRole(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		// Test files (note: function checks for /test/ and /tests/ with slashes)
		{"foo_test.go", "test"},
		{"foo.test.ts", "test"},
		{"app/test/helper.go", "test"},
		{"app/tests/unit.py", "test"},

		// Config files
		{"config.json", "config"},
		{"settings.yaml", "config"},

		// Docs
		{"README.md", "unknown"},
		{"app/docs/guide.md", "unknown"},

		// Vendor (needs slash prefix: /vendor/, /node_modules/)
		{"app/vendor/lib.go", "unknown"},
		{"app/node_modules/pkg/index.js", "unknown"},

		// Entry points
		{"app/cmd/main.go", "entrypoint"},
		{"main.go", "entrypoint"},
		{"src/index.ts", "entrypoint"},

		// Core (needs slash prefix: /internal/, /pkg/, /lib/, /src/)
		{"app/internal/query/engine.go", "core"},
		{"app/pkg/utils/helper.go", "core"},
		{"app/lib/auth.py", "core"},
		{"app/src/services/user.ts", "core"},

		// Unknown
		{"random.txt", "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := classifyFileRole(tc.path)
			if result != tc.expected {
				t.Errorf("classifyFileRole(%q) = %q, expected %q", tc.path, result, tc.expected)
			}
		})
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"file.go", "go"},
		{"file.ts", "typescript"},
		{"file.tsx", "typescript"},
		{"file.js", "javascript"},
		{"file.jsx", "javascript"},
		{"file.py", "python"},
		{"file.rs", "rust"},
		{"file.java", "java"},
		{"file.rb", "ruby"},
		{"file.swift", "swift"},
		{"file.kt", "kotlin"},
		{"file.c", "c"},
		{"file.cpp", "cpp"},
		{"file.cs", "csharp"},
		{"file.php", "php"},
		{"file.txt", ""},
		{"file", ""},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := detectLanguage(tc.path)
			if result != tc.expected {
				t.Errorf("detectLanguage(%q) = %q, expected %q", tc.path, result, tc.expected)
			}
		})
	}
}

func TestTailTimestamp(t *testing.T) {
	tests := []struct {
		name     string
		commits  []git.CommitInfo
		expected string
	}{
		{
			name:     "empty list",
			commits:  []git.CommitInfo{},
			expected: "",
		},
		{
			name: "single commit",
			commits: []git.CommitInfo{
				{Timestamp: "2024-01-15T10:00:00Z"},
			},
			expected: "2024-01-15T10:00:00Z",
		},
		{
			name: "multiple commits returns last",
			commits: []git.CommitInfo{
				{Timestamp: "2024-01-15T10:00:00Z"},
				{Timestamp: "2024-01-14T10:00:00Z"},
				{Timestamp: "2024-01-13T10:00:00Z"},
			},
			expected: "2024-01-13T10:00:00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tailTimestamp(tt.commits)
			if result != tt.expected {
				t.Errorf("tailTimestamp() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestComputeExplainFileConfidence(t *testing.T) {
	tests := []struct {
		name     string
		basis    []ConfidenceBasisItem
		expected float64
	}{
		{
			name:     "no backends",
			basis:    []ConfidenceBasisItem{},
			expected: 0.69,
		},
		{
			name: "scip missing",
			basis: []ConfidenceBasisItem{
				{Backend: "scip", Status: "missing"},
			},
			expected: 0.69,
		},
		{
			name: "scip available",
			basis: []ConfidenceBasisItem{
				{Backend: "scip", Status: "available"},
			},
			expected: 0.89,
		},
		{
			name: "scip and git available",
			basis: []ConfidenceBasisItem{
				{Backend: "scip", Status: "available"},
				{Backend: "git", Status: "available"},
			},
			expected: 0.89,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeExplainFileConfidence(tt.basis)
			if result != tt.expected {
				t.Errorf("computeExplainFileConfidence() = %f, want %f", result, tt.expected)
			}
		})
	}
}

func TestBuildFileOneLiner(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		role        string
		symbolCount int
		keySymbols  []string
		contains    string
	}{
		{
			name:        "test file",
			path:        "internal/query/engine_test.go",
			role:        "test",
			symbolCount: 5,
			keySymbols:  []string{"TestEngine"},
			contains:    "test file",
		},
		{
			name:        "config file",
			path:        "config.json",
			role:        "config",
			symbolCount: 0,
			keySymbols:  nil,
			contains:    "configuration file",
		},
		{
			name:        "entrypoint",
			path:        "cmd/ckb/main.go",
			role:        "entrypoint",
			symbolCount: 3,
			keySymbols:  []string{"main"},
			contains:    "entry point",
		},
		{
			name:        "core with symbols",
			path:        "internal/query/engine.go",
			role:        "core",
			symbolCount: 10,
			keySymbols:  []string{"Engine", "Query"},
			contains:    "Engine",
		},
		{
			name:        "core without symbols",
			path:        "internal/query/empty.go",
			role:        "core",
			symbolCount: 3,
			keySymbols:  nil,
			contains:    "core implementation",
		},
		{
			name:        "unknown with symbols",
			path:        "random/file.go",
			role:        "unknown",
			symbolCount: 5,
			keySymbols:  []string{"Foo"},
			contains:    "Foo",
		},
		{
			name:        "unknown without symbols",
			path:        "random/file.go",
			role:        "unknown",
			symbolCount: 3,
			keySymbols:  nil,
			contains:    "3 symbols",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildFileOneLiner(tt.path, tt.role, tt.symbolCount, tt.keySymbols)
			if result == "" {
				t.Error("buildFileOneLiner returned empty string")
			}
			if tt.contains != "" && !containsSubstring(result, tt.contains) {
				t.Errorf("buildFileOneLiner() = %q, expected to contain %q", result, tt.contains)
			}
		})
	}
}

func TestIsExportedSymbol(t *testing.T) {
	tests := []struct {
		name       string
		symbolName string
		visibility string
		language   string
		expected   bool
	}{
		// Explicit visibility
		{"explicit public", "foo", "public", "go", true},
		{"explicit private", "Foo", "private", "go", false},
		{"explicit protected", "Foo", "protected", "go", false},

		// Go conventions
		{"go exported", "Engine", "", "go", true},
		{"go unexported", "engine", "", "go", false},
		{"go exported method", "GetUser", "", "go", true},

		// Python conventions
		{"python public", "engine", "", "python", true},
		{"python private", "_engine", "", "python", false},
		{"python dunder", "__init__", "", "python", false},

		// JS/TS conventions
		{"js public", "engine", "", "javascript", true},
		{"js private", "_engine", "", "javascript", false},
		{"ts public", "Engine", "", "typescript", true},
		{"ts private", "_Engine", "", "typescript", false},

		// Unknown language
		{"unknown lang", "foo", "", "rust", false},
		{"empty name", "", "", "go", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isExportedSymbol(tt.symbolName, tt.visibility, tt.language)
			if result != tt.expected {
				t.Errorf("isExportedSymbol(%q, %q, %q) = %v, want %v",
					tt.symbolName, tt.visibility, tt.language, result, tt.expected)
			}
		})
	}
}

func TestComputePathScore(t *testing.T) {
	tests := []struct {
		name       string
		pathType   string
		pathLength int
		confidence float64
		minScore   float64
	}{
		{"cli short high conf", "cli", 1, 1.0, 90},
		{"api short high conf", "api", 1, 1.0, 70},
		{"job short", "job", 1, 1.0, 50},
		{"event short", "event", 1, 1.0, 40},
		{"test short", "test", 1, 1.0, 30},
		{"unknown short", "unknown", 1, 1.0, 10},
		{"cli long path", "cli", 5, 1.0, 60},
		{"cli low confidence", "cli", 1, 0.5, 45},
		{"very long path floors at zero", "cli", 30, 1.0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computePathScore(tt.pathType, tt.pathLength, tt.confidence)
			if result < tt.minScore {
				t.Errorf("computePathScore(%q, %d, %f) = %f, want >= %f",
					tt.pathType, tt.pathLength, tt.confidence, result, tt.minScore)
			}
		})
	}
}

func TestIsTestFilePath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"internal/query/engine_test.go", true},
		{"src/Button.test.tsx", true},
		{"src/test/fixtures/data.go", true},
		{"src/tests/unit/api.go", true},
		{"internal/query/engine.go", false},
		{"src/Button.tsx", false},
		{"main.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isTestFilePath(tt.path)
			if result != tt.expected {
				t.Errorf("isTestFilePath(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestGenerateConceptDescription(t *testing.T) {
	tests := []struct {
		name        string
		category    string
		occurrences int
		fileCount   int
		contains    string
	}{
		{"Cache", "technical", 10, 5, "Technical concept"},
		{"Factory", "pattern", 3, 2, "Design pattern"},
		{"User", "domain", 50, 15, "Domain concept"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateConceptDescription(tt.name, tt.category, tt.occurrences, tt.fileCount)
			if !containsSubstring(result, tt.contains) {
				t.Errorf("generateConceptDescription() = %q, expected to contain %q", result, tt.contains)
			}
		})
	}
}

func TestLooksLikeAPIHandlerName(t *testing.T) {
	yes := []string{"Handle", "HandleFoo", "handleFoo", "ServeHTTP", "serve", "serve_http", "UserHandler", "handler", "http_handler"}
	no := []string{"serverConfig", "ServerConfig", "handled", "served", "Handlers", "reserve", "Observe", "", "unhandledError"}
	for _, n := range yes {
		if !looksLikeAPIHandlerName(n) {
			t.Errorf("%q should look like a handler", n)
		}
	}
	for _, n := range no {
		if looksLikeAPIHandlerName(n) {
			t.Errorf("%q should not look like a handler", n)
		}
	}
}

func TestCapCallGraph(t *testing.T) {
	root := "root"
	nodes := []CallGraphNode{{ID: "deep", Depth: 2}, {ID: "b", Depth: 1}, {ID: root}, {ID: "a", Depth: 1}}
	edges := []CallGraphEdge{{From: "a", To: root}, {From: "b", To: root}, {From: "deep", To: "a"}}

	got, gotEdges, trunc := capCallGraph(root, append([]CallGraphNode(nil), nodes...), edges, 3)
	if len(got) != 3 || got[0].ID != root || got[1].ID != "a" || got[2].ID != "b" {
		t.Errorf("nodes = %+v, want root and the two at depth 1", got)
	}
	if len(gotEdges) != 2 {
		t.Errorf("edges = %+v, want the edge to the dropped node gone", gotEdges)
	}
	if trunc == nil || trunc.OriginalCount != 4 || trunc.ReturnedCount != 3 {
		t.Errorf("truncation = %+v", trunc)
	}

	if _, _, trunc := capCallGraph(root, nodes, edges, 10); trunc != nil {
		t.Errorf("truncation reported when nothing was cut: %+v", trunc)
	}
	if got, _, _ := capCallGraph(root, append(nodes, make([]CallGraphNode, 40)...), edges, 0); len(got) != defaultCallGraphNodes {
		t.Errorf("default cap kept %d nodes", len(got))
	}
}
