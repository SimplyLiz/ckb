package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Helper to create a temp directory with files
func setupTestDir(t *testing.T, files []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", f, err)
		}
		if err := os.WriteFile(path, []byte(""), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", f, err)
		}
	}
	return dir
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		wantLang Language
		wantOk   bool
	}{
		{
			name:     "Go project",
			files:    []string{"go.mod", "main.go"},
			wantLang: LangGo,
			wantOk:   true,
		},
		{
			name:     "TypeScript project",
			files:    []string{"package.json", "tsconfig.json", "src/index.ts"},
			wantLang: LangTypeScript,
			wantOk:   true,
		},
		{
			name:     "JavaScript project (no tsconfig)",
			files:    []string{"package.json", "src/index.js"},
			wantLang: LangJavaScript,
			wantOk:   true,
		},
		{
			name:     "Python project with pyproject.toml",
			files:    []string{"pyproject.toml", "src/main.py"},
			wantLang: LangPython,
			wantOk:   true,
		},
		{
			name:     "Python project with requirements.txt",
			files:    []string{"requirements.txt", "app.py"},
			wantLang: LangPython,
			wantOk:   true,
		},
		{
			name:     "Rust project",
			files:    []string{"Cargo.toml", "src/main.rs"},
			wantLang: LangRust,
			wantOk:   true,
		},
		{
			name:     "Java Maven project",
			files:    []string{"pom.xml", "src/main/java/App.java"},
			wantLang: LangJava,
			wantOk:   true,
		},
		{
			name:     "Java Gradle project",
			files:    []string{"build.gradle", "src/main/java/App.java"},
			wantLang: LangJava,
			wantOk:   true,
		},
		{
			name:     "Kotlin project",
			files:    []string{"build.gradle.kts", "src/main/kotlin/App.kt"},
			wantLang: LangKotlin,
			wantOk:   true,
		},
		{
			name:     "Dart project",
			files:    []string{"pubspec.yaml", "lib/main.dart"},
			wantLang: LangDart,
			wantOk:   true,
		},
		{
			name:     "PHP project",
			files:    []string{"composer.json", "src/index.php"},
			wantLang: LangPHP,
			wantOk:   true,
		},
		{
			name:     "Ruby project with Gemfile",
			files:    []string{"Gemfile", "lib/app.rb"},
			wantLang: LangRuby,
			wantOk:   true,
		},
		{
			name:     "C++ project with compile_commands.json",
			files:    []string{"compile_commands.json", "src/main.cpp"},
			wantLang: LangCpp,
			wantOk:   true,
		},
		{
			name:     "C++ project with compile_commands in build/",
			files:    []string{"build/compile_commands.json", "src/main.cpp"},
			wantLang: LangCpp,
			wantOk:   true,
		},
		{
			name:     "Unknown project",
			files:    []string{"README.md", "random.txt"},
			wantLang: LangUnknown,
			wantOk:   false,
		},
		{
			name:     "Empty directory",
			files:    []string{},
			wantLang: LangUnknown,
			wantOk:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			lang, _, ok := DetectLanguage(dir)
			if lang != tt.wantLang {
				t.Errorf("DetectLanguage() lang = %v, want %v", lang, tt.wantLang)
			}
			if ok != tt.wantOk {
				t.Errorf("DetectLanguage() ok = %v, want %v", ok, tt.wantOk)
			}
		})
	}
}

func TestDetectAllLanguages_MultiLanguage(t *testing.T) {
	tests := []struct {
		name         string
		files        []string
		wantPrimary  Language
		wantAllCount int
	}{
		{
			name:         "Go + TypeScript monorepo",
			files:        []string{"go.mod", "package.json", "tsconfig.json"},
			wantPrimary:  LangGo, // Go has higher priority
			wantAllCount: 2,
		},
		{
			name:         "Python + Ruby",
			files:        []string{"requirements.txt", "Gemfile"},
			wantPrimary:  LangPython, // Python has higher priority
			wantAllCount: 2,
		},
		{
			name:         "Java + Kotlin (same build system)",
			files:        []string{"build.gradle", "build.gradle.kts"},
			wantPrimary:  LangJava, // Java comes first in manifests
			wantAllCount: 2,
		},
		{
			name:         "Single language",
			files:        []string{"go.mod"},
			wantPrimary:  LangGo,
			wantAllCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			primary, _, allLangs := DetectAllLanguages(dir)
			if primary != tt.wantPrimary {
				t.Errorf("DetectAllLanguages() primary = %v, want %v", primary, tt.wantPrimary)
			}
			if len(allLangs) != tt.wantAllCount {
				t.Errorf("DetectAllLanguages() allLangs count = %d, want %d (got %v)", len(allLangs), tt.wantAllCount, allLangs)
			}
		})
	}
}

func TestDetectLanguage_GlobPatterns(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		wantLang Language
	}{
		{
			name:     "C# with .csproj at root",
			files:    []string{"MyApp.csproj", "Program.cs"},
			wantLang: LangCSharp,
		},
		{
			name:     "C# with .csproj in subdirectory",
			files:    []string{"src/MyApp/MyApp.csproj", "src/MyApp/Program.cs"},
			wantLang: LangCSharp,
		},
		{
			name:     "C# with .sln file",
			files:    []string{"MySolution.sln", "src/MyApp/MyApp.csproj"},
			wantLang: LangCSharp,
		},
		{
			name:     "Ruby with .gemspec",
			files:    []string{"mylib.gemspec", "lib/mylib.rb"},
			wantLang: LangRuby,
		},
		{
			name:     "Ruby with .gemspec in subdirectory",
			files:    []string{"gems/mylib/mylib.gemspec", "gems/mylib/lib/mylib.rb"},
			wantLang: LangRuby,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			lang, _, ok := DetectLanguage(dir)
			if !ok {
				t.Errorf("DetectLanguage() failed to detect language")
				return
			}
			if lang != tt.wantLang {
				t.Errorf("DetectLanguage() = %v, want %v", lang, tt.wantLang)
			}
		})
	}
}

func TestDetectLanguage_SkipsIgnoredDirs(t *testing.T) {
	// Files in node_modules, .git, vendor should be ignored
	files := []string{
		"node_modules/some-pkg/package.json",
		".git/config",
		"vendor/somelib/go.mod",
		"README.md",
	}
	dir := setupTestDir(t, files)

	lang, _, ok := DetectLanguage(dir)
	if ok {
		t.Errorf("DetectLanguage() should not detect language from ignored dirs, got %v", lang)
	}
}

func TestDetectLanguage_DepthLimit(t *testing.T) {
	// File at depth 4 should not be found (maxScanDepth = 3)
	files := []string{
		"a/b/c/d/MyApp.csproj", // depth 4
		"README.md",
	}
	dir := setupTestDir(t, files)

	lang, _, ok := DetectLanguage(dir)
	if ok && lang == LangCSharp {
		t.Errorf("DetectLanguage() should not find .csproj at depth 4")
	}

	// But depth 3 should work
	files2 := []string{
		"a/b/c/MyApp.csproj", // depth 3
	}
	dir2 := setupTestDir(t, files2)

	lang2, _, ok2 := DetectLanguage(dir2)
	if !ok2 || lang2 != LangCSharp {
		t.Errorf("DetectLanguage() should find .csproj at depth 3, got %v, ok=%v", lang2, ok2)
	}
}

func TestFindCompileCommands(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		wantPath string
	}{
		{
			name:     "At root",
			files:    []string{"compile_commands.json"},
			wantPath: "compile_commands.json",
		},
		{
			name:     "In build/",
			files:    []string{"build/compile_commands.json"},
			wantPath: "build/compile_commands.json",
		},
		{
			name:     "In out/",
			files:    []string{"out/compile_commands.json"},
			wantPath: "out/compile_commands.json",
		},
		{
			name:     "In cmake-build-debug/",
			files:    []string{"cmake-build-debug/compile_commands.json"},
			wantPath: "cmake-build-debug/compile_commands.json",
		},
		{
			name:     "In build/subdir/ (glob)",
			files:    []string{"build/Debug/compile_commands.json"},
			wantPath: "build/Debug/compile_commands.json",
		},
		{
			name:     "Not found",
			files:    []string{"src/main.cpp"},
			wantPath: "",
		},
		{
			name:     "Root takes precedence over build/",
			files:    []string{"compile_commands.json", "build/compile_commands.json"},
			wantPath: "compile_commands.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			got := FindCompileCommands(dir)
			if got != tt.wantPath {
				t.Errorf("FindCompileCommands() = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

func TestBuildCppCommand(t *testing.T) {
	tests := []struct {
		name           string
		files          []string
		overrideCompdb string
		wantCmd        string
		wantErr        bool
	}{
		{
			name:    "Auto-detect at root",
			files:   []string{"compile_commands.json"},
			wantCmd: "scip-clang --compdb-path=compile_commands.json",
		},
		{
			name:    "Auto-detect in build/",
			files:   []string{"build/compile_commands.json"},
			wantCmd: "scip-clang --compdb-path=build/compile_commands.json",
		},
		{
			name:           "Override path",
			files:          []string{"custom/path/compile_commands.json"},
			overrideCompdb: "custom/path/compile_commands.json",
			wantCmd:        "scip-clang --compdb-path=custom/path/compile_commands.json",
		},
		{
			name:    "Not found",
			files:   []string{"src/main.cpp"},
			wantCmd: "",
			wantErr: false, // Returns empty string, not error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			got, err := BuildCppCommand(dir, tt.overrideCompdb)
			if (err != nil) != tt.wantErr {
				t.Errorf("BuildCppCommand() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantCmd {
				t.Errorf("BuildCppCommand() = %q, want %q", got, tt.wantCmd)
			}
		})
	}
}

func TestBuildRubyCommand(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		wantCmd string
		wantErr bool
	}{
		{
			name:    "With Gemfile and sorbet/config",
			files:   []string{"Gemfile", "sorbet/config"},
			wantCmd: "bundle exec scip-ruby",
		},
		{
			name:    "With Gemfile, no sorbet",
			files:   []string{"Gemfile"},
			wantCmd: "bundle exec scip-ruby .",
		},
		{
			name:    "No Gemfile",
			files:   []string{"lib/app.rb"},
			wantCmd: "scip-ruby .",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			got, err := BuildRubyCommand(dir)

			// Skip bundle check error in tests (bundle may not be installed)
			if err != nil && tt.files[0] == "Gemfile" {
				// This is expected if bundle is not installed
				t.Skipf("Skipping: bundle not installed")
				return
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("BuildRubyCommand() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantCmd {
				t.Errorf("BuildRubyCommand() = %q, want %q", got, tt.wantCmd)
			}
		})
	}
}

func TestValidatePHPSetup(t *testing.T) {
	tests := []struct {
		name        string
		files       []string
		wantWarning bool
		wantErr     bool
	}{
		{
			name:        "Full setup",
			files:       []string{"composer.json", "composer.lock", "vendor/bin/scip-php"},
			wantWarning: false,
			wantErr:     false,
		},
		{
			name:        "Missing composer.lock (warning only)",
			files:       []string{"composer.json", "vendor/bin/scip-php"},
			wantWarning: true,
			wantErr:     false,
		},
		{
			name:        "Missing vendor/bin/scip-php (error)",
			files:       []string{"composer.json", "composer.lock"},
			wantWarning: false,
			wantErr:     true,
		},
		{
			name:        "Missing both (warning + error)",
			files:       []string{"composer.json"},
			wantWarning: true,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			warning, err := ValidatePHPSetup(dir)

			gotWarning := warning != ""
			gotErr := err != nil

			if gotWarning != tt.wantWarning {
				t.Errorf("ValidatePHPSetup() warning = %v, want %v", gotWarning, tt.wantWarning)
			}
			if gotErr != tt.wantErr {
				t.Errorf("ValidatePHPSetup() err = %v, want %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestGetIndexerInfo(t *testing.T) {
	tests := []struct {
		lang      Language
		wantNil   bool
		wantCheck string
	}{
		{LangGo, false, "scip-go"},
		{LangTypeScript, false, "scip-typescript"},
		{LangJavaScript, false, "scip-typescript"},
		{LangPython, false, "scip-python"},
		{LangRust, false, "rust-analyzer"},
		{LangJava, false, "scip-java"},
		{LangKotlin, false, "scip-java"},
		{LangCpp, false, "scip-clang"},
		{LangDart, false, "dart"},
		{LangRuby, false, "scip-ruby"},
		{LangCSharp, false, "scip-dotnet"},
		{LangPHP, false, "vendor/bin/scip-php"},
		{LangUnknown, true, ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.lang), func(t *testing.T) {
			info := GetIndexerInfo(tt.lang)
			if tt.wantNil {
				if info != nil {
					t.Errorf("GetIndexerInfo(%v) = %+v, want nil", tt.lang, info)
				}
				return
			}
			if info == nil {
				t.Errorf("GetIndexerInfo(%v) = nil, want non-nil", tt.lang)
				return
			}
			if info.CheckCommand != tt.wantCheck {
				t.Errorf("GetIndexerInfo(%v).CheckCommand = %q, want %q", tt.lang, info.CheckCommand, tt.wantCheck)
			}
		})
	}
}

func TestLanguageDisplayName(t *testing.T) {
	tests := []struct {
		lang Language
		want string
	}{
		{LangGo, "Go"},
		{LangTypeScript, "TypeScript"},
		{LangJavaScript, "JavaScript"},
		{LangPython, "Python"},
		{LangRust, "Rust"},
		{LangJava, "Java"},
		{LangKotlin, "Kotlin"},
		{LangCpp, "C/C++"},
		{LangDart, "Dart"},
		{LangRuby, "Ruby"},
		{LangCSharp, "C#"},
		{LangPHP, "PHP"},
		{LangUnknown, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.lang), func(t *testing.T) {
			got := LanguageDisplayName(tt.lang)
			if got != tt.want {
				t.Errorf("LanguageDisplayName(%v) = %q, want %q", tt.lang, got, tt.want)
			}
		})
	}
}

func TestDetectJSorTS(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		wantLang Language
	}{
		{
			name:     "Has tsconfig.json",
			files:    []string{"package.json", "tsconfig.json"},
			wantLang: LangTypeScript,
		},
		{
			name:     "Has .ts files in root",
			files:    []string{"package.json", "index.ts"},
			wantLang: LangTypeScript,
		},
		{
			name:     "Has .ts files in src/",
			files:    []string{"package.json", "src/index.ts"},
			wantLang: LangTypeScript,
		},
		{
			name:     "Only .js files",
			files:    []string{"package.json", "index.js"},
			wantLang: LangJavaScript,
		},
		{
			name:     "No JS/TS files",
			files:    []string{"package.json"},
			wantLang: LangJavaScript, // Defaults to JS
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			got := detectJSorTS(dir)
			if got != tt.wantLang {
				t.Errorf("detectJSorTS() = %v, want %v", got, tt.wantLang)
			}
		})
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()

	config := &ProjectConfig{
		Language:     LangGo,
		Indexer:      "scip-go",
		ManifestPath: "go.mod",
	}

	// Save
	if err := SaveConfig(dir, config); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Verify file exists
	configPath := filepath.Join(dir, ".ckb", "project.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatalf("Config file not created at %s", configPath)
	}

	// Load
	loaded, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if loaded.Language != config.Language {
		t.Errorf("LoadConfig().Language = %v, want %v", loaded.Language, config.Language)
	}
	if loaded.Indexer != config.Indexer {
		t.Errorf("LoadConfig().Indexer = %v, want %v", loaded.Indexer, config.Indexer)
	}
	if loaded.ManifestPath != config.ManifestPath {
		t.Errorf("LoadConfig().ManifestPath = %v, want %v", loaded.ManifestPath, config.ManifestPath)
	}
}

// --- Subdirectory and monorepo detection tests ---

func TestDetectLanguage_SubdirectoryManifest(t *testing.T) {
	tests := []struct {
		name         string
		files        []string
		wantLang     Language
		wantManifest string
	}{
		{
			name:         "go.mod in src/cli/ (ShellAI layout)",
			files:        []string{"README.md", "src/cli/go.mod", "src/cli/main.go"},
			wantLang:     LangGo,
			wantManifest: "src/cli/go.mod",
		},
		{
			name:         "go.mod in root takes priority over subdir",
			files:        []string{"go.mod", "src/cli/go.mod"},
			wantLang:     LangGo,
			wantManifest: "go.mod",
		},
		{
			name:         "Cargo.toml in packages/core/",
			files:        []string{"README.md", "packages/core/Cargo.toml"},
			wantLang:     LangRust,
			wantManifest: "packages/core/Cargo.toml",
		},
		{
			name:         "package.json in src/web/ with tsconfig at root",
			files:        []string{"README.md", "src/web/package.json", "tsconfig.json"},
			wantLang:     LangTypeScript,
			wantManifest: "src/web/package.json",
		},
		{
			name:         "pyproject.toml in backend/",
			files:        []string{"README.md", "backend/pyproject.toml"},
			wantLang:     LangPython,
			wantManifest: "backend/pyproject.toml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			lang, manifest, ok := DetectLanguage(dir)
			if !ok {
				t.Fatalf("DetectLanguage() failed to detect language")
			}
			if lang != tt.wantLang {
				t.Errorf("lang = %v, want %v", lang, tt.wantLang)
			}
			if manifest != tt.wantManifest {
				t.Errorf("manifest = %q, want %q", manifest, tt.wantManifest)
			}
		})
	}
}

func TestDetectAllLanguages_MultiLanguageMonorepo(t *testing.T) {
	tests := []struct {
		name         string
		files        []string
		wantPrimary  Language
		wantAllCount int
		wantAll      []Language
	}{
		{
			name: "Go + TypeScript (ShellAI layout)",
			files: []string{
				"src/cli/go.mod",
				"src/cli/main.go",
				"src/web/package.json",
				"src/web/tsconfig.json",
			},
			wantPrimary:  LangGo,
			wantAllCount: 2,
			wantAll:      []Language{LangGo, LangTypeScript},
		},
		{
			name: "Go + Python in separate subdirs",
			files: []string{
				"services/api/go.mod",
				"scripts/pyproject.toml",
			},
			wantPrimary:  LangGo,
			wantAllCount: 2,
			wantAll:      []Language{LangGo, LangPython},
		},
		{
			name: "Single language in subdir",
			files: []string{
				"src/cli/go.mod",
				"README.md",
			},
			wantPrimary:  LangGo,
			wantAllCount: 1,
			wantAll:      []Language{LangGo},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			primary, _, allLangs := DetectAllLanguages(dir)
			if primary != tt.wantPrimary {
				t.Errorf("primary = %v, want %v", primary, tt.wantPrimary)
			}
			if len(allLangs) != tt.wantAllCount {
				t.Errorf("allLangs count = %d, want %d (got %v)", len(allLangs), tt.wantAllCount, allLangs)
			}
			for _, want := range tt.wantAll {
				found := false
				for _, got := range allLangs {
					if got == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected %v in allLangs %v", want, allLangs)
				}
			}
		})
	}
}

func TestDetectLanguage_SkipsExampleAndTestDirs(t *testing.T) {
	// Manifests in example/test/doc directories should NOT be detected
	files := []string{
		"README.md",
		"examples/dart-app/pubspec.yaml",
		"example/python-demo/pyproject.toml",
		"testdata/fixtures/go.mod",
		"docs/tutorial/package.json",
	}
	dir := setupTestDir(t, files)

	_, _, ok := DetectLanguage(dir)
	if ok {
		t.Error("should not detect language from examples/testdata/docs directories")
	}
}

func TestFindManifestForLanguage(t *testing.T) {
	tests := []struct {
		name         string
		files        []string
		lang         Language
		wantManifest string
	}{
		{
			name:         "Go in subdir",
			files:        []string{"src/cli/go.mod", "src/web/package.json"},
			lang:         LangGo,
			wantManifest: "src/cli/go.mod",
		},
		{
			name:         "TypeScript in subdir",
			files:        []string{"src/cli/go.mod", "src/web/package.json"},
			lang:         LangTypeScript,
			wantManifest: "src/web/package.json",
		},
		{
			name:         "Go at root",
			files:        []string{"go.mod", "main.go"},
			lang:         LangGo,
			wantManifest: "go.mod",
		},
		{
			name:         "Language not present",
			files:        []string{"go.mod"},
			lang:         LangRust,
			wantManifest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupTestDir(t, tt.files)
			got := FindManifestForLanguage(dir, tt.lang)
			if got != tt.wantManifest {
				t.Errorf("FindManifestForLanguage() = %q, want %q", got, tt.wantManifest)
			}
		})
	}
}

func TestFindManifest_DepthAndSkipDirs(t *testing.T) {
	t.Run("finds at depth 2", func(t *testing.T) {
		dir := setupTestDir(t, []string{"src/cli/go.mod"})
		got := findManifest(dir, "go.mod")
		if got != filepath.Join("src", "cli", "go.mod") {
			t.Errorf("findManifest() = %q, want src/cli/go.mod", got)
		}
	})

	t.Run("finds at depth 3", func(t *testing.T) {
		dir := setupTestDir(t, []string{"a/b/c/go.mod"})
		got := findManifest(dir, "go.mod")
		if got != filepath.Join("a", "b", "c", "go.mod") {
			t.Errorf("findManifest() = %q, want a/b/c/go.mod", got)
		}
	})

	t.Run("not found at depth 4", func(t *testing.T) {
		dir := setupTestDir(t, []string{"a/b/c/d/go.mod"})
		got := findManifest(dir, "go.mod")
		if got != "" {
			t.Errorf("findManifest() should not find at depth 4, got %q", got)
		}
	})

	t.Run("root takes priority", func(t *testing.T) {
		dir := setupTestDir(t, []string{"go.mod", "src/cli/go.mod"})
		got := findManifest(dir, "go.mod")
		if got != "go.mod" {
			t.Errorf("findManifest() = %q, want root go.mod", got)
		}
	})

	t.Run("skips vendor", func(t *testing.T) {
		dir := setupTestDir(t, []string{"vendor/lib/go.mod"})
		got := findManifest(dir, "go.mod")
		if got != "" {
			t.Errorf("findManifest() should skip vendor, got %q", got)
		}
	})

	t.Run("skips examples", func(t *testing.T) {
		dir := setupTestDir(t, []string{"examples/demo/go.mod"})
		got := findManifest(dir, "go.mod")
		if got != "" {
			t.Errorf("findManifest() should skip examples, got %q", got)
		}
	})

	t.Run("skips node_modules", func(t *testing.T) {
		dir := setupTestDir(t, []string{"node_modules/pkg/package.json"})
		got := findManifest(dir, "package.json")
		if got != "" {
			t.Errorf("findManifest() should skip node_modules, got %q", got)
		}
	})
}

func TestLoadConfig_NotFound(t *testing.T) {
	dir := t.TempDir()

	_, err := LoadConfig(dir)
	if err == nil {
		t.Errorf("LoadConfig() should return error for missing config")
	}
}

func TestDetectAllLanguages_SkipsHiddenAndVendoredDirs(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"go.mod", ".claude/worktrees/x/package.json", "third_party/lib/Cargo.toml", ".github/actions/a/package.json"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lang, _, all := DetectAllLanguages(root)
	if lang != LangGo || len(all) != 1 {
		t.Errorf("detected %v (primary %s), want Go alone: hidden and vendored manifests are not this project's", all, lang)
	}
}

// A repo of scripts has no manifest; the language of most tracked source files is
// picked, and git-ignored files don't count.
func TestDetectLanguage_SourceFilesWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "Temp/\n")
	write("exp/a.py", "x = 1\n")
	write("exp/b.py", "y = 2\n")
	write("mods/hook.ts", "export const r = 1\n")
	for i := 0; i < 5; i++ {
		write(filepath.Join("Temp", string(rune('a'+i))+".ts"), "export const s = 1\n")
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	lang, from, ok := DetectLanguage(dir)
	if !ok || lang != LangPython {
		t.Fatalf("DetectLanguage = %v %q %v, want python", lang, from, ok)
	}
	if from != "2 tracked .py files, no manifest" {
		t.Errorf("from = %q", from)
	}
}

func TestDetectLanguage_NoSourcesNoManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	_ = cmd.Run()
	if lang, _, ok := DetectLanguage(dir); ok {
		t.Fatalf("DetectLanguage = %v, want unknown", lang)
	}
}
