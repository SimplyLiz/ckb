package incremental

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

func TestParseGitDiffNUL(t *testing.T) {
	// Create a detector with config that includes test files
	d := &ChangeDetector{
		config: &Config{
			IndexTests: true, // Include _test.go files for testing
		},
	}

	tests := []struct {
		name     string
		input    []byte
		expected []ChangedFile
	}{
		{
			name:     "empty input",
			input:    []byte{},
			expected: nil,
		},
		{
			name:  "single added file",
			input: []byte("A\x00main.go\x00"),
			expected: []ChangedFile{
				{Path: "main.go", ChangeType: ChangeAdded},
			},
		},
		{
			name:  "single modified file",
			input: []byte("M\x00internal/foo.go\x00"),
			expected: []ChangedFile{
				{Path: "internal/foo.go", ChangeType: ChangeModified},
			},
		},
		{
			name:  "single deleted file",
			input: []byte("D\x00old.go\x00"),
			expected: []ChangedFile{
				{Path: "old.go", ChangeType: ChangeDeleted},
			},
		},
		{
			name:  "rename go to go",
			input: []byte("R100\x00old.go\x00new.go\x00"),
			expected: []ChangedFile{
				{Path: "new.go", OldPath: "old.go", ChangeType: ChangeRenamed},
			},
		},
		{
			name:  "rename go to non-go (treated as delete)",
			input: []byte("R100\x00main.go\x00main.txt\x00"),
			expected: []ChangedFile{
				{Path: "main.go", ChangeType: ChangeDeleted},
			},
		},
		{
			name:  "rename non-go to go (treated as add)",
			input: []byte("R100\x00main.txt\x00main.go\x00"),
			expected: []ChangedFile{
				{Path: "main.go", ChangeType: ChangeAdded},
			},
		},
		{
			name:     "rename non-go to non-go (ignored)",
			input:    []byte("R100\x00old.txt\x00new.txt\x00"),
			expected: nil,
		},
		{
			name:  "copy creates new file",
			input: []byte("C100\x00original.go\x00copy.go\x00"),
			expected: []ChangedFile{
				{Path: "copy.go", ChangeType: ChangeAdded},
			},
		},
		{
			name:  "multiple changes",
			input: []byte("A\x00new.go\x00M\x00modified.go\x00D\x00deleted.go\x00"),
			expected: []ChangedFile{
				{Path: "new.go", ChangeType: ChangeAdded},
				{Path: "modified.go", ChangeType: ChangeModified},
				{Path: "deleted.go", ChangeType: ChangeDeleted},
			},
		},
		{
			name:  "path with spaces",
			input: []byte("A\x00path with spaces/file.go\x00"),
			expected: []ChangedFile{
				{Path: "path with spaces/file.go", ChangeType: ChangeAdded},
			},
		},
		{
			name:     "non-go file ignored",
			input:    []byte("A\x00readme.md\x00"),
			expected: nil,
		},
		{
			name:  "mixed go and non-go",
			input: []byte("A\x00main.go\x00M\x00readme.md\x00D\x00old.go\x00"),
			expected: []ChangedFile{
				{Path: "main.go", ChangeType: ChangeAdded},
				{Path: "old.go", ChangeType: ChangeDeleted},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := d.parseGitDiffNUL(tc.input)

			if len(result) != len(tc.expected) {
				t.Errorf("expected %d changes, got %d", len(tc.expected), len(result))
				return
			}

			for i, exp := range tc.expected {
				got := result[i]
				if got.Path != exp.Path {
					t.Errorf("change %d: expected path %q, got %q", i, exp.Path, got.Path)
				}
				if got.OldPath != exp.OldPath {
					t.Errorf("change %d: expected oldPath %q, got %q", i, exp.OldPath, got.OldPath)
				}
				if got.ChangeType != exp.ChangeType {
					t.Errorf("change %d: expected type %q, got %q", i, exp.ChangeType, got.ChangeType)
				}
			}
		})
	}
}

func TestDeduplicateChanges(t *testing.T) {
	d := &ChangeDetector{}

	tests := []struct {
		name     string
		input    []ChangedFile
		expected []ChangedFile
	}{
		{
			name:     "empty",
			input:    nil,
			expected: nil,
		},
		{
			name: "no duplicates",
			input: []ChangedFile{
				{Path: "a.go", ChangeType: ChangeAdded},
				{Path: "b.go", ChangeType: ChangeModified},
			},
			expected: []ChangedFile{
				{Path: "a.go", ChangeType: ChangeAdded},
				{Path: "b.go", ChangeType: ChangeModified},
			},
		},
		{
			name: "duplicate keeps last",
			input: []ChangedFile{
				{Path: "a.go", ChangeType: ChangeAdded},
				{Path: "a.go", ChangeType: ChangeModified},
			},
			expected: []ChangedFile{
				{Path: "a.go", ChangeType: ChangeModified},
			},
		},
		{
			name: "multiple duplicates",
			input: []ChangedFile{
				{Path: "a.go", ChangeType: ChangeAdded},
				{Path: "b.go", ChangeType: ChangeAdded},
				{Path: "a.go", ChangeType: ChangeModified},
				{Path: "b.go", ChangeType: ChangeDeleted},
			},
			expected: []ChangedFile{
				{Path: "a.go", ChangeType: ChangeModified},
				{Path: "b.go", ChangeType: ChangeDeleted},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := d.deduplicateChanges(tc.input)

			if len(result) != len(tc.expected) {
				t.Errorf("expected %d changes, got %d", len(tc.expected), len(result))
				return
			}

			for i, exp := range tc.expected {
				got := result[i]
				if got.Path != exp.Path || got.ChangeType != exp.ChangeType {
					t.Errorf("change %d: expected {%s, %s}, got {%s, %s}",
						i, exp.Path, exp.ChangeType, got.Path, got.ChangeType)
				}
			}
		})
	}
}

func TestIsGoFile(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		indexTests bool
		excludes   []string
		expected   bool
	}{
		{"go file", "main.go", false, nil, true},
		{"non-go file", "main.txt", false, nil, false},
		{"test file with tests disabled", "main_test.go", false, nil, false},
		{"test file with tests enabled", "main_test.go", true, nil, true},
		{"excluded pattern", "vendor/foo.go", false, []string{"vendor"}, false},
		{"nested path", "internal/pkg/file.go", false, nil, true},
		{"excluded nested", "vendor/pkg/file.go", false, []string{"vendor"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := &ChangeDetector{
				config: &Config{
					IndexTests: tc.indexTests,
					Excludes:   tc.excludes,
				},
			}
			result := d.isSourceFile(tc.path)
			if result != tc.expected {
				t.Errorf("isSourceFile(%q) = %v, want %v", tc.path, result, tc.expected)
			}
		})
	}
}

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		excludes []string
		expected bool
	}{
		{"no excludes", "main.go", nil, false},
		{"exact match", "vendor", []string{"vendor"}, true},
		{"directory prefix", "vendor/foo/bar.go", []string{"vendor"}, true},
		{"glob match", "test_data.go", []string{"test_*.go"}, true},
		{"no match", "main.go", []string{"vendor", "testdata"}, false},
		{"multiple excludes first matches", "vendor/x.go", []string{"vendor", "testdata"}, true},
		{"multiple excludes second matches", "testdata/x.go", []string{"vendor", "testdata"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := &ChangeDetector{
				config: &Config{
					Excludes: tc.excludes,
				},
			}
			result := d.isExcluded(tc.path)
			if result != tc.expected {
				t.Errorf("isExcluded(%q) = %v, want %v", tc.path, result, tc.expected)
			}
		})
	}
}

func setupTestDetector(t *testing.T) (*ChangeDetector, *Store, string, func()) {
	t.Helper()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "incremental-detector-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create .ckb directory
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create .ckb dir: %v", err)
	}

	// Create logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Open database
	db, err := storage.Open(tmpDir, logger)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to open database: %v", err)
	}

	store := NewStore(db, logger)
	config := DefaultConfig()
	detector := NewChangeDetector(tmpDir, store, config, logger)

	cleanup := func() {
		db.Close() //nolint:errcheck // Test cleanup
		os.RemoveAll(tmpDir)
	}

	return detector, store, tmpDir, cleanup
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure git user for commits
	configCmd := exec.Command("git", "config", "user.email", "test@test.com")
	configCmd.Dir = dir
	configCmd.Run() //nolint:errcheck

	configCmd2 := exec.Command("git", "config", "user.name", "Test")
	configCmd2.Dir = dir
	configCmd2.Run() //nolint:errcheck
}

func TestNewChangeDetector(t *testing.T) {
	detector, _, _, cleanup := setupTestDetector(t)
	defer cleanup()

	if detector == nil {
		t.Fatal("expected non-nil detector")
	}
	if detector.store == nil {
		t.Error("expected non-nil store")
	}
	if detector.config == nil {
		t.Error("expected non-nil config")
	}
}

func TestNewChangeDetector_NilConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "detector-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Pass nil config
	detector := NewChangeDetector(tmpDir, nil, nil, logger)

	if detector.config == nil {
		t.Fatal("expected non-nil config after initialization")
	}
}

func TestIsGitRepo_NonGitDir(t *testing.T) {
	detector, _, _, cleanup := setupTestDetector(t)
	defer cleanup()

	// Temp dir is not a git repo
	if detector.isGitRepo() {
		t.Error("expected isGitRepo=false for non-git directory")
	}
}

func TestIsGitRepo_GitDir(t *testing.T) {
	detector, _, tmpDir, cleanup := setupTestDetector(t)
	defer cleanup()

	// Initialize git repo
	initGitRepo(t, tmpDir)

	if !detector.isGitRepo() {
		t.Error("expected isGitRepo=true for git directory")
	}
}

func TestGetCurrentCommit_NonGitRepo(t *testing.T) {
	detector, _, _, cleanup := setupTestDetector(t)
	defer cleanup()

	commit := detector.GetCurrentCommit()
	if commit != "" {
		t.Errorf("expected empty commit for non-git repo, got %q", commit)
	}
}

func TestGetCurrentCommit_GitRepo(t *testing.T) {
	detector, _, tmpDir, cleanup := setupTestDetector(t)
	defer cleanup()

	// Initialize git repo
	initGitRepo(t, tmpDir)

	// Create a file and commit it
	testFile := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = tmpDir
	if err := addCmd.Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}

	commitCmd := exec.Command("git", "commit", "-m", "initial")
	commitCmd.Dir = tmpDir
	if err := commitCmd.Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	commit := detector.GetCurrentCommit()
	if commit == "" {
		t.Error("expected non-empty commit after git commit")
	}
	if len(commit) < 7 {
		t.Errorf("expected commit hash, got %q", commit)
	}
}

func TestDetectChanges_NonGitFallback(t *testing.T) {
	detector, store, tmpDir, cleanup := setupTestDetector(t)
	defer cleanup()

	// Not a git repo, should fall back to hash-based detection
	// Add a Go file
	testFile := filepath.Join(tmpDir, "new.go")
	if err := os.WriteFile(testFile, []byte("package main\nfunc main() {}"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// No files in store, so the new file should be detected as added
	changes, err := detector.DetectChanges("")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	// Should find the new file
	found := false
	for _, c := range changes {
		if c.Path == "new.go" && c.ChangeType == ChangeAdded {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to detect new.go as added")
	}

	// Now simulate that the file was indexed
	if err := store.SaveFileState(&IndexedFile{Path: "new.go", Hash: "somehash"}); err != nil {
		t.Fatalf("SaveFileState failed: %v", err)
	}

	// Modify the file
	if err := os.WriteFile(testFile, []byte("package main\nfunc main() { println(1) }"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	changes, err = detector.DetectChanges("")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	// Should find the file as modified
	found = false
	for _, c := range changes {
		if c.Path == "new.go" && c.ChangeType == ChangeModified {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to detect new.go as modified")
	}
}

func TestDetectHashChanges_DeletedFile(t *testing.T) {
	detector, store, _, cleanup := setupTestDetector(t)
	defer cleanup()

	// Simulate a file that was previously indexed
	if err := store.SaveFileState(&IndexedFile{Path: "deleted.go", Hash: "abc"}); err != nil {
		t.Fatalf("SaveFileState failed: %v", err)
	}

	// File doesn't exist on disk, should be detected as deleted
	changes, err := detector.DetectChanges("")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Path == "deleted.go" && c.ChangeType == ChangeDeleted {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to detect deleted.go as deleted")
	}
}

func TestDetectChanges_GitBased(t *testing.T) {
	detector, store, tmpDir, cleanup := setupTestDetector(t)
	defer cleanup()

	// Initialize git repo
	initGitRepo(t, tmpDir)

	// Create and commit initial file
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = tmpDir
	addCmd.Run() //nolint:errcheck

	commitCmd := exec.Command("git", "commit", "-m", "initial")
	commitCmd.Dir = tmpDir
	commitCmd.Run() //nolint:errcheck

	// Get commit hash
	commit := detector.GetCurrentCommit()

	// Set the indexed commit in store
	if err := store.SetLastIndexedCommit(commit); err != nil {
		t.Fatalf("SetLastIndexedCommit failed: %v", err)
	}

	// No changes yet
	changes, err := detector.DetectChanges("")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}

	// Now modify the file (uncommitted)
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc main() {}"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	// Should detect uncommitted change
	changes, err = detector.DetectChanges("")
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}

	found := false
	for _, c := range changes {
		if c.Path == "main.go" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to detect main.go as changed")
	}
}

func TestHasDirtyWorkingTree(t *testing.T) {
	detector, _, tmpDir, cleanup := setupTestDetector(t)
	defer cleanup()

	// Initialize git repo
	initGitRepo(t, tmpDir)

	// Create and commit a file
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = tmpDir
	addCmd.Run() //nolint:errcheck

	commitCmd := exec.Command("git", "commit", "-m", "initial")
	commitCmd.Dir = tmpDir
	commitCmd.Run() //nolint:errcheck

	// Working tree should be clean
	if detector.HasDirtyWorkingTree() {
		t.Error("expected clean working tree after commit")
	}

	// Make a modification
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc foo() {}"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	// Working tree should be dirty
	if !detector.HasDirtyWorkingTree() {
		t.Error("expected dirty working tree after modification")
	}
}

func TestHashFile(t *testing.T) {
	detector, _, tmpDir, cleanup := setupTestDetector(t)
	defer cleanup()

	testFile := filepath.Join(tmpDir, "test.go")
	content := []byte("package main\n")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	hash1, err := detector.hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile failed: %v", err)
	}
	if hash1 == "" {
		t.Error("expected non-empty hash")
	}

	// Same content should give same hash
	hash2, err := detector.hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile failed: %v", err)
	}
	if hash1 != hash2 {
		t.Error("expected same hash for same content")
	}

	// Different content should give different hash
	if err := os.WriteFile(testFile, []byte("package foo"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}
	hash3, err := detector.hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile failed: %v", err)
	}
	if hash1 == hash3 {
		t.Error("expected different hash for different content")
	}
}

func TestHashFile_NonExistent(t *testing.T) {
	detector, _, tmpDir, cleanup := setupTestDetector(t)
	defer cleanup()

	_, err := detector.hashFile(filepath.Join(tmpDir, "nonexistent.go"))
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}
