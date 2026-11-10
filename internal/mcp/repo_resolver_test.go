package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractPathHint(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string]interface{}
		expected string
	}{
		{
			name:     "empty params",
			params:   map[string]interface{}{},
			expected: "",
		},
		{
			name:     "filePath param",
			params:   map[string]interface{}{"filePath": "internal/mcp/server.go"},
			expected: "internal/mcp/server.go",
		},
		{
			name:     "path param",
			params:   map[string]interface{}{"path": "cmd/ckb/main.go"},
			expected: "cmd/ckb/main.go",
		},
		{
			name:     "targetPath param",
			params:   map[string]interface{}{"targetPath": "/absolute/path/file.go"},
			expected: "/absolute/path/file.go",
		},
		{
			name:     "target with path separator treated as path",
			params:   map[string]interface{}{"target": "internal/query/engine.go"},
			expected: "internal/query/engine.go",
		},
		{
			name:     "target without separator skipped (symbol name)",
			params:   map[string]interface{}{"target": "MCPServer.GetEngine"},
			expected: "",
		},
		{
			name:     "moduleId param",
			params:   map[string]interface{}{"moduleId": "internal/mcp"},
			expected: "internal/mcp",
		},
		{
			name:     "priority order - filePath wins",
			params:   map[string]interface{}{"filePath": "first.go", "path": "second.go"},
			expected: "first.go",
		},
		{
			name:     "empty string value skipped",
			params:   map[string]interface{}{"filePath": "", "path": "fallback.go"},
			expected: "fallback.go",
		},
		{
			name:     "non-string value skipped",
			params:   map[string]interface{}{"filePath": 123, "path": "fallback.go"},
			expected: "fallback.go",
		},
		{
			name:     "target with backslash treated as path",
			params:   map[string]interface{}{"target": "internal\\mcp\\server.go"},
			expected: "internal\\mcp\\server.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPathHint(tt.params)
			if result != tt.expected {
				t.Errorf("extractPathHint(%v) = %q, want %q", tt.params, result, tt.expected)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "simple path",
			input: "/Users/test/project",
		},
		{
			name:  "path with dots",
			input: "/Users/test/../test/project",
		},
		{
			name:  "path with double slashes",
			input: "/Users//test/project",
		},
		{
			name:  "relative path",
			input: "internal/mcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizePath(tt.input)
			// Should return a cleaned path
			if result == "" {
				t.Errorf("normalizePath(%q) returned empty string", tt.input)
			}
			// Should be cleaned (no double slashes, no . or ..)
			cleaned := filepath.Clean(tt.input)
			// Result should be at least as clean as filepath.Clean
			if filepath.Clean(result) != result {
				t.Errorf("normalizePath(%q) = %q is not clean", tt.input, result)
			}
			_ = cleaned // used for documentation
		})
	}
}

func TestNormalizePath_NonexistentPath(t *testing.T) {
	// normalizePath should handle nonexistent paths gracefully
	result := normalizePath("/nonexistent/path/that/does/not/exist")
	if result == "" {
		t.Error("normalizePath should return cleaned path even for nonexistent paths")
	}
	expected := filepath.Clean("/nonexistent/path/that/does/not/exist")
	if result != expected {
		t.Errorf("normalizePath returned %q, expected %q", result, expected)
	}
}

func TestNormalizePath_Symlink(t *testing.T) {
	// Create a temp directory with a symlink
	tmpDir, err := os.MkdirTemp("", "normalizepath-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create actual directory
	actualDir := filepath.Join(tmpDir, "actual")
	if err := os.Mkdir(actualDir, 0755); err != nil {
		t.Fatalf("Failed to create actual dir: %v", err)
	}

	// Create symlink
	linkDir := filepath.Join(tmpDir, "link")
	if err := os.Symlink(actualDir, linkDir); err != nil {
		t.Skipf("Symlinks not supported: %v", err)
	}

	// normalizePath should resolve the symlink
	result := normalizePath(linkDir)

	// Result should point to actual directory (after resolving symlinks)
	resultResolved, _ := filepath.EvalSymlinks(result)
	actualResolved, _ := filepath.EvalSymlinks(actualDir)

	if resultResolved != actualResolved {
		t.Errorf("normalizePath(%q) = %q, expected to resolve to %q", linkDir, result, actualDir)
	}
}

func TestResolveRepoForPath_EmptyHint(t *testing.T) {
	server := &MCPServer{
		roots: newRootsManager(),
	}

	result := server.resolveRepoForPath("")
	if result != "" {
		t.Errorf("resolveRepoForPath('') = %q, want empty string", result)
	}
}

func TestResolveRepoForPath_AbsolutePath(t *testing.T) {
	// Use the current repo as a test case
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get cwd: %v", err)
	}

	server := &MCPServer{
		roots: newRootsManager(),
	}

	// This file exists in a git repo
	result := server.resolveRepoForPath(cwd)

	// Should return a git root (non-empty if we're in a git repo)
	// Don't fail if not in a git repo, just skip
	if result == "" {
		t.Skip("Not running in a git repository")
	}

	// The result should be a parent of cwd
	if !isParentOrEqual(result, cwd) {
		t.Errorf("resolveRepoForPath(%q) = %q, expected a parent directory", cwd, result)
	}
}

// isParentOrEqual checks if parent is a parent directory of child (or equal)
func isParentOrEqual(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)

	if parent == child {
		return true
	}

	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}

	// If relative path doesn't start with "..", parent is an ancestor
	return len(rel) > 0 && rel[0] != '.'
}

// A relative path that exists in both the main checkout (the client root) and
// the linked worktree switchProject moved to must stay in the worktree.
func TestResolveRelativeHint_ActiveRepoBeatsClientRoot(t *testing.T) {
	base := t.TempDir()
	mainRepo := filepath.Join(base, "main")
	worktree := filepath.Join(mainRepo, ".claude", "worktrees", "review")
	for _, dir := range []string{mainRepo, worktree} {
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "src", "a.ts"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(mainRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := resolveRelativeHint("src/a.ts", worktree, []string{mainRepo})
	if normalizePath(got) != normalizePath(worktree) {
		t.Errorf("resolved to %q, want the active worktree %q", got, worktree)
	}

	// With no active repo the client root still resolves.
	got = resolveRelativeHint("src/a.ts", "", []string{mainRepo})
	if normalizePath(got) != normalizePath(mainRepo) {
		t.Errorf("resolved to %q, want the client root %q", got, mainRepo)
	}
}
