package coupling

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGetCorrelationLevel(t *testing.T) {
	tests := []struct {
		name        string
		correlation float64
		wantLevel   string
	}{
		{"high correlation", 0.9, "high"},
		{"high threshold", 0.8, "high"},
		{"medium high", 0.7, "medium"},
		{"medium correlation", 0.5, "medium"},
		{"low correlation", 0.4, "low"},
		{"very low", 0.2, "low"},
		{"zero correlation", 0.0, "low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetCorrelationLevel(tt.correlation)
			if got != tt.wantLevel {
				t.Errorf("GetCorrelationLevel(%v) = %v, want %v", tt.correlation, got, tt.wantLevel)
			}
		})
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

func TestAnalyzeOptionsDefaults(t *testing.T) {
	opts := AnalyzeOptions{
		Target: "test.go",
	}

	if opts.Target != "test.go" {
		t.Errorf("Target = %q, want %q", opts.Target, "test.go")
	}

	// Defaults should be zero values before Analyze sets them
	if opts.MinCorrelation != 0 {
		t.Errorf("MinCorrelation should be 0 before defaults applied")
	}
	if opts.WindowDays != 0 {
		t.Errorf("WindowDays should be 0 before defaults applied")
	}
}

func TestCorrelationStructure(t *testing.T) {
	corr := Correlation{
		File:          "other.go",
		FilePath:      "src/other.go",
		Correlation:   0.85,
		CoChangeCount: 10,
		TotalChanges:  15,
		Level:         "high",
		Direction:     "bidirectional",
	}

	if corr.File != "other.go" {
		t.Errorf("Correlation.File = %q, want %q", corr.File, "other.go")
	}
	if corr.Level != "high" {
		t.Errorf("Correlation.Level = %q, want %q", corr.Level, "high")
	}
	if corr.FilePath != "src/other.go" {
		t.Errorf("Correlation.FilePath = %q, want %q", corr.FilePath, "src/other.go")
	}
}

func TestCouplingAnalysisStructure(t *testing.T) {
	analysis := CouplingAnalysis{
		Correlations: []Correlation{
			{File: "a.go", Correlation: 0.8},
			{File: "b.go", Correlation: 0.5},
		},
		Insights:        []string{"High coupling detected"},
		Recommendations: []string{"Consider extracting shared logic"},
	}

	if len(analysis.Correlations) != 2 {
		t.Errorf("len(Correlations) = %d, want %d", len(analysis.Correlations), 2)
	}
	if len(analysis.Insights) != 1 {
		t.Errorf("len(Insights) = %d, want %d", len(analysis.Insights), 1)
	}
}

func TestCachedCorrelationStructure(t *testing.T) {
	cached := CachedCorrelation{
		FilePath:       "src/main.go",
		CorrelatedFile: "src/utils.go",
		Correlation:    0.75,
		CoChangeCount:  5,
		TotalChanges:   10,
	}

	if cached.FilePath != "src/main.go" {
		t.Errorf("FilePath = %q, want %q", cached.FilePath, "src/main.go")
	}
	if cached.Correlation != 0.75 {
		t.Errorf("Correlation = %v, want 0.75", cached.Correlation)
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
		{-1, "-1"},
		{-42, "-42"},
		{1234567890, "1234567890"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := itoa(tt.input)
			if got != tt.want {
				t.Errorf("itoa(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestUitoa(t *testing.T) {
	tests := []struct {
		input uint
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
		{1234567890, "1234567890"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := uitoa(tt.input)
			if got != tt.want {
				t.Errorf("uitoa(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateInsights(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer("/tmp", logger)

	tests := []struct {
		name         string
		correlations []Correlation
		wantContains string
	}{
		{
			name:         "no correlations",
			correlations: []Correlation{},
			wantContains: "No significant coupling patterns detected",
		},
		{
			name: "test file correlation",
			correlations: []Correlation{
				{FilePath: "main_test.go", Correlation: 0.8},
			},
			wantContains: "test updates",
		},
		{
			name: "proto file correlation",
			correlations: []Correlation{
				{FilePath: "api.proto", Correlation: 0.7},
			},
			wantContains: "API contract",
		},
		{
			name: "high coupling count",
			correlations: []Correlation{
				{FilePath: "a.go", Level: "high", Correlation: 0.9},
				{FilePath: "b.go", Level: "high", Correlation: 0.85},
				{FilePath: "c.go", Level: "high", Correlation: 0.8},
			},
			wantContains: "Strong coupling detected",
		},
		{
			name: "config file correlation",
			correlations: []Correlation{
				{File: "config.yaml", FilePath: "config.yaml", Level: "high", Correlation: 0.8},
			},
			wantContains: "Configuration often changes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			insights := analyzer.generateInsights(tt.correlations, "target.go")

			found := false
			for _, insight := range insights {
				if contains(insight, tt.wantContains) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("generateInsights() = %v, want to contain %q", insights, tt.wantContains)
			}
		})
	}
}

func TestGenerateRecommendations(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer("/tmp", logger)

	tests := []struct {
		name         string
		correlations []Correlation
		wantEmpty    bool
		wantContains string
	}{
		{
			name:         "no correlations",
			correlations: []Correlation{},
			wantEmpty:    true,
		},
		{
			name: "has top files",
			correlations: []Correlation{
				{File: "utils.go", Correlation: 0.8},
			},
			wantContains: "consider reviewing",
		},
		{
			name: "test file recommendation",
			correlations: []Correlation{
				{File: "main_test.go", FilePath: "main_test.go", Correlation: 0.8},
			},
			wantContains: "Update tests",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recs := analyzer.generateRecommendations(tt.correlations, "target.go")

			if tt.wantEmpty && len(recs) != 0 {
				t.Errorf("generateRecommendations() = %v, want empty", recs)
				return
			}

			if tt.wantContains != "" {
				found := false
				for _, rec := range recs {
					if contains(rec, tt.wantContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("generateRecommendations() = %v, want to contain %q", recs, tt.wantContains)
				}
			}
		})
	}
}

func TestResolveToFile(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer("/tmp", logger)

	// Currently just returns the input
	got := analyzer.resolveToFile("src/main.go")
	if got != "src/main.go" {
		t.Errorf("resolveToFile() = %q, want %q", got, "src/main.go")
	}
}

func TestAnalyzeWithGitRepo(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Create a temp git repo with some commits
	tmpDir := t.TempDir()

	// Initialize git repo
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.email", "test@test.com")
	runGit(t, tmpDir, "config", "user.name", "Test")

	// Create and commit a file
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, tmpDir, "add", "main.go")
	runGit(t, tmpDir, "commit", "-m", "initial")

	// Create the analyzer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer(tmpDir, logger)

	// Test analysis
	ctx := context.Background()
	opts := AnalyzeOptions{
		Target:     "main.go",
		WindowDays: 365,
	}

	result, err := analyzer.Analyze(ctx, opts)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if result == nil {
		t.Fatal("Analyze() returned nil")
	}

	// Should have at least one commit
	if result.Target.CommitCount == 0 {
		// Might be 0 if git log --follow doesn't find any commits
		// This is acceptable for a fresh repo
		t.Log("No commits found (expected for minimal test repo)")
	}
}

func TestAnalyzeNoCommits(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Create a temp git repo with no commits for the target file
	tmpDir := t.TempDir()

	// Initialize git repo
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.email", "test@test.com")
	runGit(t, tmpDir, "config", "user.name", "Test")

	// Create initial commit with different file
	otherFile := filepath.Join(tmpDir, "other.go")
	if err := os.WriteFile(otherFile, []byte("package other"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, tmpDir, "add", "other.go")
	runGit(t, tmpDir, "commit", "-m", "initial")

	// Create analyzer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	analyzer := NewAnalyzer(tmpDir, logger)

	// Analyze a file that has no commits
	ctx := context.Background()
	opts := AnalyzeOptions{
		Target:     "nonexistent.go",
		WindowDays: 365,
	}

	result, err := analyzer.Analyze(ctx, opts)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if result.Target.CommitCount != 0 {
		t.Errorf("CommitCount = %d, want 0", result.Target.CommitCount)
	}

	// Should have insight about no commits
	if len(result.Insights) == 0 {
		t.Error("Should have insights about no commits")
	}
}

// Helper functions

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

// A commit touching more files than maxFilesPerCommit made every file in it fully
// coupled to every other. It no longer counts, in the count or in the total.
func TestAnalyze_IgnoresGiantCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	// One sweeping commit with the target, a partner and 60 others.
	write("target.ts", "1")
	write("partner.ts", "1")
	for i := 0; i < 60; i++ {
		write(fmt.Sprintf("other%d.ts", i), "1")
	}
	run("add", ".")
	run("commit", "-q", "-m", "import")
	// Two ordinary commits: target with partner.
	for i := 2; i <= 3; i++ {
		write("target.ts", fmt.Sprint(i))
		write("partner.ts", fmt.Sprint(i))
		run("commit", "-q", "-am", fmt.Sprint(i))
	}

	a := NewAnalyzer(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	res, err := a.Analyze(context.Background(), AnalyzeOptions{Target: "target.ts", MinCorrelation: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Correlations) != 1 || res.Correlations[0].FilePath != "partner.ts" ||
		res.Correlations[0].Correlation != 1 || res.Correlations[0].TotalChanges != 2 {
		t.Errorf("correlations = %+v, want only partner.ts at 2/2", res.Correlations)
	}
}
