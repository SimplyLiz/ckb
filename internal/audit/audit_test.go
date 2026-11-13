package audit

import (
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestGetRiskLevel(t *testing.T) {
	tests := []struct {
		score     float64
		wantLevel string
	}{
		{90, RiskLevelCritical},
		{80, RiskLevelCritical},
		{79, RiskLevelHigh},
		{60, RiskLevelHigh},
		{59, RiskLevelMedium},
		{40, RiskLevelMedium},
		{39, RiskLevelLow},
		{0, RiskLevelLow},
	}

	for _, tt := range tests {
		t.Run(tt.wantLevel, func(t *testing.T) {
			got := GetRiskLevel(tt.score)
			if got != tt.wantLevel {
				t.Errorf("GetRiskLevel(%v) = %q, want %q", tt.score, got, tt.wantLevel)
			}
		})
	}
}

func TestRiskWeightsSum(t *testing.T) {
	// Risk weights should sum to 1.0 (with floating point tolerance)
	var total float64
	for _, weight := range RiskWeights {
		total += weight
	}

	if math.Abs(total-1.0) > 0.0001 {
		t.Errorf("RiskWeights sum = %v, want 1.0", total)
	}
}

func TestRiskWeightsComplete(t *testing.T) {
	// Ensure all factors have weights
	factors := []string{
		FactorComplexity,
		FactorTestCoverage,
		FactorBusFactor,
		FactorStaleness,
		FactorSecuritySensitive,
		FactorErrorRate,
		FactorCoChangeCoupling,
		FactorChurn,
	}

	for _, factor := range factors {
		if _, ok := RiskWeights[factor]; !ok {
			t.Errorf("Missing weight for factor: %s", factor)
		}
	}
}

func TestSecurityKeywords(t *testing.T) {
	// Ensure security keywords list is not empty
	if len(SecurityKeywords) == 0 {
		t.Error("SecurityKeywords should not be empty")
	}

	// Check for essential keywords
	// Bare "token" is gone on purpose: in LLM code it's a domain term.
	essential := []string{"password", "secret", "auth", "access_token", "api_key"}
	for _, kw := range essential {
		found := false
		for _, sk := range SecurityKeywords {
			if sk == kw {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Missing essential security keyword: %s", kw)
		}
	}
}

func TestIsSourceFile(t *testing.T) {
	tests := []struct {
		ext      string
		wantTrue bool
	}{
		{".go", true},
		{".ts", true},
		{".py", true},
		{".java", true},
		{".rs", true},
		{".txt", false},
		{".md", false},
		{".json", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := isSourceFile(tt.ext)
			if got != tt.wantTrue {
				t.Errorf("isSourceFile(%q) = %v, want %v", tt.ext, got, tt.wantTrue)
			}
		})
	}
}

func TestMinFunction(t *testing.T) {
	tests := []struct {
		a, b    float64
		wantMin float64
	}{
		{1.0, 2.0, 1.0},
		{5.0, 3.0, 3.0},
		{0.0, 1.0, 0.0},
		{-1.0, 1.0, -1.0},
		{1.5, 1.5, 1.5},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := min(tt.a, tt.b)
			if got != tt.wantMin {
				t.Errorf("min(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.wantMin)
			}
		})
	}
}

func TestRiskFactorStructure(t *testing.T) {
	factor := RiskFactor{
		Factor:       FactorComplexity,
		Value:        "42",
		Weight:       0.20,
		Contribution: 15.0,
	}

	if factor.Factor != FactorComplexity {
		t.Errorf("RiskFactor.Factor = %q, want %q", factor.Factor, FactorComplexity)
	}
	if factor.Weight != 0.20 {
		t.Errorf("RiskFactor.Weight = %v, want %v", factor.Weight, 0.20)
	}
}

func TestQuickWinStructure(t *testing.T) {
	win := QuickWin{
		Action: "Add tests",
		Target: "src/main.go",
		Effort: "medium",
		Impact: "high",
	}

	if win.Action != "Add tests" {
		t.Errorf("QuickWin.Action = %q, want %q", win.Action, "Add tests")
	}
	if win.Effort != "medium" {
		t.Errorf("QuickWin.Effort = %q, want %q", win.Effort, "medium")
	}
}

func TestRiskSummaryStructure(t *testing.T) {
	summary := RiskSummary{
		Critical: 5,
		High:     10,
		Medium:   20,
		Low:      15,
	}

	total := summary.Critical + summary.High + summary.Medium + summary.Low
	if total != 50 {
		t.Errorf("Total items = %d, want %d", total, 50)
	}
}

func TestRiskItemStructure(t *testing.T) {
	item := RiskItem{
		File:      "src/auth/login.go",
		Module:    "auth",
		RiskScore: 75.5,
		RiskLevel: RiskLevelHigh,
		Factors: []RiskFactor{
			{Factor: FactorComplexity, Value: "45", Contribution: 15.0},
		},
		Recommendation: "Consider refactoring to reduce complexity",
	}

	if item.File != "src/auth/login.go" {
		t.Errorf("RiskItem.File = %q, want %q", item.File, "src/auth/login.go")
	}
	if item.RiskLevel != RiskLevelHigh {
		t.Errorf("RiskItem.RiskLevel = %q, want %q", item.RiskLevel, RiskLevelHigh)
	}
}

func TestAuditOptionsStructure(t *testing.T) {
	opts := AuditOptions{
		RepoRoot:  "/path/to/repo",
		MinScore:  50.0,
		Limit:     25,
		Factor:    FactorComplexity,
		QuickWins: true,
	}

	if opts.MinScore != 50.0 {
		t.Errorf("AuditOptions.MinScore = %v, want %v", opts.MinScore, 50.0)
	}
	if opts.QuickWins != true {
		t.Error("AuditOptions.QuickWins should be true")
	}
}

func TestNewAnalyzer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer("/path/to/repo", logger)

	if analyzer == nil {
		t.Fatal("NewAnalyzer returned nil")
	}
	if analyzer.repoRoot != "/path/to/repo" {
		t.Errorf("repoRoot = %q, want %q", analyzer.repoRoot, "/path/to/repo")
	}
}

func TestGetComplexity(t *testing.T) {
	tmpDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer(tmpDir, logger)

	// Create a file with known complexity
	testFile := filepath.Join(tmpDir, "test.go")
	content := `package main

func main() {
	if true {
		for i := 0; i < 10; i++ {
			if i > 5 && i < 8 {
				switch i {
				case 6:
					println("six")
				case 7:
					println("seven")
				}
			}
		}
	}
}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	complexity := analyzer.getComplexityHeuristic(testFile)
	// Should detect: 2 if, 1 for, 1 switch, 2 case, 1 &&
	// Base complexity 1 + 2 + 1 + 1 + 2 + 1 = 8
	if complexity < 5 {
		t.Errorf("getComplexityHeuristic() = %d, want >= 5", complexity)
	}
}

func TestGetComplexityNonexistent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer("/tmp", logger)

	complexity := analyzer.getComplexityHeuristic("/nonexistent/file.go")
	if complexity != 0 {
		t.Errorf("getComplexityHeuristic() for nonexistent file = %d, want 0", complexity)
	}
}

func TestHasTestFile(t *testing.T) {
	tmpDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer(tmpDir, logger)

	// Create a source file and its test file
	srcFile := filepath.Join(tmpDir, "main.go")
	testFile := filepath.Join(tmpDir, "main_test.go")

	if err := os.WriteFile(srcFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	if !analyzer.hasTestFile("main.go") {
		t.Error("hasTestFile() should return true for main.go")
	}

	// Check for file without test
	if analyzer.hasTestFile("notest.go") {
		t.Error("hasTestFile() should return false for notest.go")
	}
}

func TestDetectSecurityKeywords(t *testing.T) {
	tmpDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer(tmpDir, logger)

	// Create a file with security keywords
	securityFile := filepath.Join(tmpDir, "auth.go")
	content := `package auth

func Login(password string) {
	token := generateToken(password)
	secret := getSecret()
}
`
	if err := os.WriteFile(securityFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	keywords := analyzer.detectSecurityKeywords(securityFile)

	// Should detect: password, token, secret
	if len(keywords) < 3 {
		t.Errorf("detectSecurityKeywords() found %d keywords, want >= 3", len(keywords))
	}

	// Check specific keywords
	hasPassword := false
	for _, kw := range keywords {
		if kw == "password" {
			hasPassword = true
			break
		}
	}
	if !hasPassword {
		t.Error("detectSecurityKeywords() should detect 'password'")
	}
}

func TestDetectSecurityKeywordsNonexistent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer("/tmp", logger)

	keywords := analyzer.detectSecurityKeywords("/nonexistent/file.go")
	if keywords != nil {
		t.Errorf("detectSecurityKeywords() for nonexistent file = %v, want nil", keywords)
	}
}

func TestGenerateRecommendation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer("/tmp", logger)

	tests := []struct {
		name    string
		factors []RiskFactor
		want    string
	}{
		{
			name:    "no contributing factors",
			factors: []RiskFactor{{Factor: FactorComplexity, Contribution: 0}},
			want:    "",
		},
		{
			name:    "complexity top factor",
			factors: []RiskFactor{{Factor: FactorComplexity, Contribution: 20}},
			want:    "refactoring",
		},
		{
			name:    "test coverage top factor",
			factors: []RiskFactor{{Factor: FactorTestCoverage, Contribution: 15}},
			want:    "test coverage",
		},
		{
			name:    "bus factor top",
			factors: []RiskFactor{{Factor: FactorBusFactor, Contribution: 15}},
			want:    "bus factor",
		},
		{
			name:    "staleness top",
			factors: []RiskFactor{{Factor: FactorStaleness, Contribution: 10}},
			want:    "dead code",
		},
		{
			name:    "security top",
			factors: []RiskFactor{{Factor: FactorSecuritySensitive, Contribution: 15}},
			want:    "Security",
		},
		{
			name:    "coupling top",
			factors: []RiskFactor{{Factor: FactorCoChangeCoupling, Contribution: 5}},
			want:    "decoupling",
		},
		{
			name:    "churn top",
			factors: []RiskFactor{{Factor: FactorChurn, Contribution: 5}},
			want:    "churn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := analyzer.generateRecommendation(tt.factors)
			if tt.want != "" && !containsIgnoreCase(rec, tt.want) {
				t.Errorf("generateRecommendation() = %q, want to contain %q", rec, tt.want)
			}
			if tt.want == "" && rec != "" {
				t.Errorf("generateRecommendation() = %q, want empty", rec)
			}
		})
	}
}

func TestComputeSummary(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer("/tmp", logger)

	items := []RiskItem{
		{RiskLevel: RiskLevelCritical, Factors: []RiskFactor{{Factor: FactorComplexity, Contribution: 20}}},
		{RiskLevel: RiskLevelCritical, Factors: []RiskFactor{{Factor: FactorComplexity, Contribution: 18}}},
		{RiskLevel: RiskLevelHigh, Factors: []RiskFactor{{Factor: FactorTestCoverage, Contribution: 15}}},
		{RiskLevel: RiskLevelMedium, Factors: []RiskFactor{{Factor: FactorBusFactor, Contribution: 10}}},
		{RiskLevel: RiskLevelLow, Factors: []RiskFactor{{Factor: FactorChurn, Contribution: 3}}},
	}

	summary := analyzer.computeSummary(items)

	if summary.Critical != 2 {
		t.Errorf("Critical = %d, want 2", summary.Critical)
	}
	if summary.High != 1 {
		t.Errorf("High = %d, want 1", summary.High)
	}
	if summary.Medium != 1 {
		t.Errorf("Medium = %d, want 1", summary.Medium)
	}
	if summary.Low != 1 {
		t.Errorf("Low = %d, want 1", summary.Low)
	}

	// Should have complexity as top factor (appears twice)
	if len(summary.TopRiskFactors) == 0 {
		t.Error("TopRiskFactors should not be empty")
	}
}

func TestFindQuickWins(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer("/tmp", logger)

	items := []RiskItem{
		{
			File: "risky.go",
			Factors: []RiskFactor{
				{Factor: FactorTestCoverage, Contribution: 15},
				{Factor: FactorComplexity, Contribution: 15},
			},
		},
		{
			File: "owned_by_one.go",
			Factors: []RiskFactor{
				{Factor: FactorBusFactor, Contribution: 15},
			},
		},
	}

	wins := analyzer.findQuickWins(items)

	if len(wins) < 1 {
		t.Error("findQuickWins should find at least 1 quick win")
	}

	// Should include "Add tests" for the file with no tests and high complexity
	hasAddTests := false
	for _, win := range wins {
		if win.Action == "Add tests" && win.Target == "risky.go" {
			hasAddTests = true
			break
		}
	}
	if !hasAddTests {
		t.Error("findQuickWins should include 'Add tests' for risky.go")
	}
}

func TestFindSourceFiles(t *testing.T) {
	tmpDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer(tmpDir, logger)

	// Create some source files
	if err := os.MkdirAll(filepath.Join(tmpDir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "src", "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "src", "utils.ts"), []byte("export {}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# README"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create node_modules which should be skipped
	if err := os.MkdirAll(filepath.Join(tmpDir, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "node_modules", "pkg.js"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	files, err := analyzer.findSourceFiles(tmpDir)
	if err != nil {
		t.Fatalf("findSourceFiles() error = %v", err)
	}

	// Should find main.go and utils.ts, but not README.md or node_modules files
	if len(files) != 2 {
		t.Errorf("findSourceFiles() found %d files, want 2", len(files))
	}

	// Verify README.md is not included
	for _, f := range files {
		if filepath.Base(f) == "README.md" {
			t.Error("findSourceFiles() should not include README.md")
		}
		if containsIgnoreCase(f, "node_modules") {
			t.Error("findSourceFiles() should not include node_modules files")
		}
	}
}

// Helper function for case-insensitive contains
func containsIgnoreCase(s, substr string) bool {
	sLower := toLower(s)
	substrLower := toLower(substr)
	return len(sLower) >= len(substrLower) && containsStr(sLower, substrLower)
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
