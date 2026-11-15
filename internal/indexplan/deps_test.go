package indexplan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/project"
)

func TestDepsCommand(t *testing.T) {
	flutter := "name: app\ndependencies:\n  flutter:\n    sdk: flutter\n"
	pureDart := "name: lib\ndependencies:\n  path: ^1.9.0\n"
	tests := []struct {
		name    string
		lang    project.Language
		pubspec string
		config  bool
		want    string
	}{
		{"fresh Flutter clone", project.LangDart, flutter, false, "flutter pub get"},
		{"fresh Dart package", project.LangDart, pureDart, false, "dart pub get"},
		{"already fetched", project.LangDart, flutter, true, ""},
		{"not Dart", project.LangGo, flutter, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte(tt.pubspec), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.config {
				if err := os.MkdirAll(filepath.Join(dir, ".dart_tool"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".dart_tool", "package_config.json"), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := DepsCommand(&Plan{Lang: tt.lang, IndexerDir: dir})
			if got != tt.want {
				t.Errorf("DepsCommand = %q, want %q", got, tt.want)
			}
		})
	}
}

// A pub workspace member resolves into the workspace root's .dart_tool.
func TestDepsCommandWorkspaceMember(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "packages", "core")
	for _, d := range []string{filepath.Join(root, ".git"), filepath.Join(root, ".dart_tool"), member} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".dart_tool", "package_config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(member, "pubspec.yaml"), []byte("name: core\nresolution: workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DepsCommand(&Plan{Lang: project.LangDart, IndexerDir: member}); got != "" {
		t.Errorf("DepsCommand = %q, want none", got)
	}
}
