package modules

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDetectModulesWithManifest(t *testing.T) {
	// Create a temp directory with a package.json
	tempDir, err := os.MkdirTemp("", "ckb-modules-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create package.json
	packageJSON := `{"name": "test-package", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(packageJSON), 0644); err != nil {
		t.Fatalf("Failed to write package.json: %v", err)
	}

	logger := newTestLogger()
	result, err := DetectModules(tempDir, nil, nil, "test-state", logger)
	if err != nil {
		t.Fatalf("DetectModules failed: %v", err)
	}

	if result.DetectionMethod != "manifest" {
		t.Errorf("Expected detection method 'manifest', got '%s'", result.DetectionMethod)
	}

	if len(result.Modules) == 0 {
		t.Error("Expected at least one module")
	}
}

func TestDetectModulesWithExplicitRoots(t *testing.T) {
	// Create a temp directory with module directories
	tempDir, err := os.MkdirTemp("", "ckb-modules-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create module directories
	moduleDir := filepath.Join(tempDir, "mymodule")
	if err := os.MkdirAll(moduleDir, 0755); err != nil {
		t.Fatalf("Failed to create module dir: %v", err)
	}

	// Create a Go file
	if err := os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatalf("Failed to write main.go: %v", err)
	}

	logger := newTestLogger()
	result, err := DetectModules(tempDir, []string{"mymodule"}, nil, "test-state", logger)
	if err != nil {
		t.Fatalf("DetectModules failed: %v", err)
	}

	if result.DetectionMethod != "explicit" {
		t.Errorf("Expected detection method 'explicit', got '%s'", result.DetectionMethod)
	}

	if len(result.Modules) != 1 {
		t.Fatalf("Expected 1 module, got %d", len(result.Modules))
	}

	if result.Modules[0].Language != LanguageGo {
		t.Errorf("Expected language '%s', got '%s'", LanguageGo, result.Modules[0].Language)
	}
}

func TestDetectModulesWithConvention(t *testing.T) {
	// Create a temp directory with convention directories
	tempDir, err := os.MkdirTemp("", "ckb-modules-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create src directory (convention)
	srcDir := filepath.Join(tempDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("Failed to create src dir: %v", err)
	}

	logger := newTestLogger()
	result, err := DetectModules(tempDir, nil, nil, "test-state", logger)
	if err != nil {
		t.Fatalf("DetectModules failed: %v", err)
	}

	if result.DetectionMethod != "convention" {
		t.Errorf("Expected detection method 'convention', got '%s'", result.DetectionMethod)
	}

	if len(result.Modules) == 0 {
		t.Error("Expected at least one module")
	}

	// Verify src module was detected
	foundSrc := false
	for _, m := range result.Modules {
		if m.Name == "src" {
			foundSrc = true
			break
		}
	}

	if !foundSrc {
		t.Error("Expected 'src' module to be detected")
	}
}

func TestDetectModulesWithFallback(t *testing.T) {
	// Create a temp directory with arbitrary directories
	tempDir, err := os.MkdirTemp("", "ckb-modules-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create some directories
	if err := os.MkdirAll(filepath.Join(tempDir, "foo"), 0755); err != nil {
		t.Fatalf("Failed to create foo dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempDir, "bar"), 0755); err != nil {
		t.Fatalf("Failed to create bar dir: %v", err)
	}

	logger := newTestLogger()
	result, err := DetectModules(tempDir, nil, nil, "test-state", logger)
	if err != nil {
		t.Fatalf("DetectModules failed: %v", err)
	}

	if result.DetectionMethod != "fallback" {
		t.Errorf("Expected detection method 'fallback', got '%s'", result.DetectionMethod)
	}

	if len(result.Modules) != 2 {
		t.Errorf("Expected 2 modules, got %d", len(result.Modules))
	}
}

func TestDetectModulesWithIgnore(t *testing.T) {
	// Create a temp directory
	tempDir, err := os.MkdirTemp("", "ckb-modules-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create directories
	if err := os.MkdirAll(filepath.Join(tempDir, "keep"), 0755); err != nil {
		t.Fatalf("Failed to create keep dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempDir, "ignore"), 0755); err != nil {
		t.Fatalf("Failed to create ignore dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempDir, "node_modules"), 0755); err != nil {
		t.Fatalf("Failed to create node_modules dir: %v", err)
	}

	logger := newTestLogger()
	result, err := DetectModules(tempDir, nil, []string{"ignore", "node_modules"}, "test-state", logger)
	if err != nil {
		t.Fatalf("DetectModules failed: %v", err)
	}

	// Only "keep" should be detected
	if len(result.Modules) != 1 {
		t.Errorf("Expected 1 module (keep), got %d", len(result.Modules))
	}

	if result.Modules[0].Name != "keep" {
		t.Errorf("Expected module name 'keep', got '%s'", result.Modules[0].Name)
	}
}

func TestDetectModulesWithDeclared(t *testing.T) {
	// Create a temp directory
	tempDir, err := os.MkdirTemp("", "ckb-modules-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a MODULES.toml file
	modulesContent := `
version = 1

[[module]]
name = "api"
path = "internal/api"
responsibility = "HTTP API handlers"
`

	// Create internal/api directory
	if err := os.MkdirAll(filepath.Join(tempDir, "internal", "api"), 0755); err != nil {
		t.Fatalf("Failed to create internal/api dir: %v", err)
	}

	modulesPath := filepath.Join(tempDir, "MODULES.toml")
	if err := os.WriteFile(modulesPath, []byte(modulesContent), 0644); err != nil {
		t.Fatalf("Failed to write MODULES.toml: %v", err)
	}

	logger := newTestLogger()
	result, err := DetectModulesWithDeclaration(tempDir, nil, nil, "", "test-state", logger)
	if err != nil {
		t.Fatalf("DetectModulesWithDeclaration failed: %v", err)
	}

	if result.DetectionMethod != "declared" {
		t.Errorf("Expected detection method 'declared', got '%s'", result.DetectionMethod)
	}

	if len(result.Modules) != 1 {
		t.Errorf("Expected 1 module, got %d", len(result.Modules))
	}

	if result.Modules[0].Name != "api" {
		t.Errorf("Expected module name 'api', got '%s'", result.Modules[0].Name)
	}
}

func TestDetectLanguageFromFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ckb-lang-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testCases := []struct {
		name     string
		files    map[string]string
		expected string
	}{
		{
			name: "Go files",
			files: map[string]string{
				"main.go":  "package main",
				"util.go":  "package main",
				"other.go": "package main",
			},
			expected: LanguageGo,
		},
		{
			name: "TypeScript files",
			files: map[string]string{
				"index.ts":     "export {}",
				"component.ts": "export {}",
			},
			expected: LanguageTypeScript,
		},
		{
			name: "Python files",
			files: map[string]string{
				"app.py":   "print('hello')",
				"utils.py": "print('world')",
			},
			expected: LanguagePython,
		},
		{
			name: "Rust files",
			files: map[string]string{
				"main.rs": "fn main() {}",
				"lib.rs":  "mod foo;",
			},
			expected: LanguageRust,
		},
		{
			name: "Java files",
			files: map[string]string{
				"Main.java": "public class Main {}",
				"Util.java": "public class Util {}",
			},
			expected: LanguageJava,
		},
		{
			name: "Mixed - Go wins by count",
			files: map[string]string{
				"a.go":  "package a",
				"b.go":  "package b",
				"c.go":  "package c",
				"d.ts":  "export {}",
				"e.tsx": "export {}",
			},
			expected: LanguageGo,
		},
		{
			name:     "Empty directory",
			files:    map[string]string{},
			expected: LanguageUnknown,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create subdirectory for this test case
			testDir := filepath.Join(tempDir, tc.name)
			if err := os.MkdirAll(testDir, 0755); err != nil {
				t.Fatalf("Failed to create test dir: %v", err)
			}

			// Create files
			for filename, content := range tc.files {
				if err := os.WriteFile(filepath.Join(testDir, filename), []byte(content), 0644); err != nil {
					t.Fatalf("Failed to write %s: %v", filename, err)
				}
			}

			result := detectLanguageFromFiles(testDir)
			if result != tc.expected {
				t.Errorf("Expected language '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestExtractNameFromPackageJSON(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ckb-package-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testCases := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "valid package.json",
			content:  `{"name": "my-package", "version": "1.0.0"}`,
			expected: "my-package",
		},
		{
			name:     "scoped package",
			content:  `{"name": "@scope/my-package", "version": "1.0.0"}`,
			expected: "@scope/my-package",
		},
		{
			name:     "empty name",
			content:  `{"version": "1.0.0"}`,
			expected: "pkg", // fallback to directory name
		},
		{
			name:     "invalid json",
			content:  `{not valid json}`,
			expected: "pkg", // fallback to directory name
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pkgDir := filepath.Join(tempDir, "pkg")
			if err := os.MkdirAll(pkgDir, 0755); err != nil {
				t.Fatalf("Failed to create pkg dir: %v", err)
			}
			defer os.RemoveAll(pkgDir)

			pkgPath := filepath.Join(pkgDir, "package.json")
			if err := os.WriteFile(pkgPath, []byte(tc.content), 0644); err != nil {
				t.Fatalf("Failed to write package.json: %v", err)
			}

			result := extractNameFromPackageJSON(pkgPath)
			if result != tc.expected {
				t.Errorf("Expected name '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestExtractNameFromGoMod(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ckb-gomod-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testCases := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "simple module",
			content:  "module mymodule\n\ngo 1.21\n",
			expected: "mymodule",
		},
		{
			name:     "module with path",
			content:  "module github.com/user/myproject\n\ngo 1.21\n",
			expected: "myproject",
		},
		{
			name:     "module with subdirectory",
			content:  "module example.com/org/repo/subdir\n\ngo 1.21\n",
			expected: "subdir",
		},
		{
			name:     "empty file",
			content:  "",
			expected: "mod", // fallback to directory name
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			modDir := filepath.Join(tempDir, "mod")
			if err := os.MkdirAll(modDir, 0755); err != nil {
				t.Fatalf("Failed to create mod dir: %v", err)
			}
			defer os.RemoveAll(modDir)

			modPath := filepath.Join(modDir, "go.mod")
			if err := os.WriteFile(modPath, []byte(tc.content), 0644); err != nil {
				t.Fatalf("Failed to write go.mod: %v", err)
			}

			result := extractNameFromGoMod(modPath)
			if result != tc.expected {
				t.Errorf("Expected name '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestExtractNameFromCargoToml(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ckb-cargo-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testCases := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name: "simple cargo.toml",
			content: `[package]
name = "my-crate"
version = "0.1.0"
`,
			expected: "my-crate",
		},
		{
			name: "with single quotes",
			content: `[package]
name = 'my-crate'
version = '0.1.0'
`,
			expected: "my-crate",
		},
		{
			name:     "no package section",
			content:  `[dependencies]\nfoo = "1.0"\n`,
			expected: "crate", // fallback to directory name
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			crateDir := filepath.Join(tempDir, "crate")
			if err := os.MkdirAll(crateDir, 0755); err != nil {
				t.Fatalf("Failed to create crate dir: %v", err)
			}
			defer os.RemoveAll(crateDir)

			cargoPath := filepath.Join(crateDir, "Cargo.toml")
			if err := os.WriteFile(cargoPath, []byte(tc.content), 0644); err != nil {
				t.Fatalf("Failed to write Cargo.toml: %v", err)
			}

			result := extractNameFromCargoToml(cargoPath)
			if result != tc.expected {
				t.Errorf("Expected name '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestShouldIgnore(t *testing.T) {
	testCases := []struct {
		relPath   string
		ignoreMap map[string]bool
		expected  bool
	}{
		{
			relPath:   "node_modules",
			ignoreMap: map[string]bool{"node_modules": true},
			expected:  true,
		},
		{
			relPath:   "src",
			ignoreMap: map[string]bool{"node_modules": true},
			expected:  false,
		},
		{
			relPath:   "foo/node_modules",
			ignoreMap: map[string]bool{"node_modules": true},
			expected:  true,
		},
		{
			relPath:   "vendor/github.com/foo",
			ignoreMap: map[string]bool{"vendor": true},
			expected:  true,
		},
		{
			relPath:   "src/vendor",
			ignoreMap: map[string]bool{"vendor": true},
			expected:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.relPath, func(t *testing.T) {
			result := shouldIgnore(tc.relPath, tc.ignoreMap)
			if result != tc.expected {
				t.Errorf("shouldIgnore(%s) = %v, expected %v", tc.relPath, result, tc.expected)
			}
		})
	}
}

func TestGenerateModuleID(t *testing.T) {
	// Same path should produce same ID
	id1 := generateModuleID("/repo", "internal/api")
	id2 := generateModuleID("/repo", "internal/api")
	if id1 != id2 {
		t.Errorf("Same path should produce same ID: %s != %s", id1, id2)
	}

	// Different paths should produce different IDs
	id3 := generateModuleID("/repo", "internal/query")
	if id1 == id3 {
		t.Errorf("Different paths should produce different IDs: %s == %s", id1, id3)
	}

	// ID should have correct prefix (ckb:mod: per the implementation, matching declaration.go)
	expectedPrefix := "ckb:mod:"
	if len(id1) < len(expectedPrefix) || id1[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("ID should start with '%s', got %s", expectedPrefix, id1)
	}
}

func TestDetectManifestInDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ckb-manifest-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test with package.json
	t.Run("package.json", func(t *testing.T) {
		dir := filepath.Join(tempDir, "node")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0644); err != nil {
			t.Fatalf("Failed to write package.json: %v", err)
		}

		manifest, lang := detectManifestInDir(dir)
		if manifest != ManifestPackageJSON {
			t.Errorf("Expected manifest '%s', got '%s'", ManifestPackageJSON, manifest)
		}
		if lang != LanguageTypeScript {
			t.Errorf("Expected language '%s', got '%s'", LanguageTypeScript, lang)
		}
	})

	// Test with go.mod
	t.Run("go.mod", func(t *testing.T) {
		dir := filepath.Join(tempDir, "go")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644); err != nil {
			t.Fatalf("Failed to write go.mod: %v", err)
		}

		manifest, lang := detectManifestInDir(dir)
		if manifest != ManifestGoMod {
			t.Errorf("Expected manifest '%s', got '%s'", ManifestGoMod, manifest)
		}
		if lang != LanguageGo {
			t.Errorf("Expected language '%s', got '%s'", LanguageGo, lang)
		}
	})

	// Test with no manifest
	t.Run("no manifest", func(t *testing.T) {
		dir := filepath.Join(tempDir, "empty")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}

		manifest, lang := detectManifestInDir(dir)
		if manifest != ManifestNone {
			t.Errorf("Expected manifest '%s', got '%s'", ManifestNone, manifest)
		}
		if lang != LanguageUnknown {
			t.Errorf("Expected language '%s', got '%s'", LanguageUnknown, lang)
		}
	})
}

func TestNestedManifestDetection(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ckb-nested-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create nested structure with multiple package.json files
	// root/
	//   package.json (root)
	//   packages/
	//     frontend/
	//       package.json
	//     backend/
	//       package.json
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(`{"name":"root"}`), 0644); err != nil {
		t.Fatalf("Failed to write root package.json: %v", err)
	}

	frontendDir := filepath.Join(tempDir, "packages", "frontend")
	if err := os.MkdirAll(frontendDir, 0755); err != nil {
		t.Fatalf("Failed to create frontend dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(frontendDir, "package.json"), []byte(`{"name":"frontend"}`), 0644); err != nil {
		t.Fatalf("Failed to write frontend package.json: %v", err)
	}

	backendDir := filepath.Join(tempDir, "packages", "backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatalf("Failed to create backend dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backendDir, "package.json"), []byte(`{"name":"backend"}`), 0644); err != nil {
		t.Fatalf("Failed to write backend package.json: %v", err)
	}

	logger := newTestLogger()
	result, err := DetectModules(tempDir, nil, nil, "test-state", logger)
	if err != nil {
		t.Fatalf("DetectModules failed: %v", err)
	}

	// Should detect root package, but not descend into nested ones
	// (because SkipDir is returned after finding a manifest)
	if len(result.Modules) == 0 {
		t.Error("Expected at least one module")
	}
}

// BenchmarkDetectModules benchmarks module detection
func BenchmarkDetectModules(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "ckb-bench-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a moderate structure
	for i := 0; i < 10; i++ {
		dir := filepath.Join(tempDir, "module"+string(rune('a'+i)))
		if err := os.MkdirAll(dir, 0755); err != nil {
			b.Fatalf("Failed to create dir: %v", err)
		}
		for j := 0; j < 5; j++ {
			filename := filepath.Join(dir, "file"+string(rune('0'+j))+".go")
			if err := os.WriteFile(filename, []byte("package main"), 0644); err != nil {
				b.Fatalf("Failed to write file: %v", err)
			}
		}
	}

	logger := newTestLogger()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DetectModules(tempDir, nil, nil, "bench-state", logger)
	}
}

func TestDetectModules_SkipsHiddenDirectories(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"api/package.json", ".claude/worktrees/x/package.json", ".github/actions/a/package.json"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(`{"name":"m"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := DetectModules(root, nil, nil, "state", newTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Modules) != 1 || result.Modules[0].RootPath != "api" {
		var paths []string
		for _, m := range result.Modules {
			paths = append(paths, m.RootPath)
		}
		t.Errorf("modules = %v, want only api: a manifest in a hidden directory is a tool's, not ours", paths)
	}
}
