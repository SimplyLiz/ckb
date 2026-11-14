package query

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindLayersConfig(t *testing.T) {
	root := t.TempDir()
	if got := findLayersConfig(root); got != "" {
		t.Fatalf("empty repo: got %q, want \"\"", got)
	}

	write := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("[layers]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	legacy := write(".cartographer/layers.toml")
	if got := findLayersConfig(root); got != legacy {
		t.Errorf("legacy only: got %q, want %q", got, legacy)
	}
	dotDir := write(".codecartographer/layers.toml")
	if got := findLayersConfig(root); got != dotDir {
		t.Errorf(".codecartographer beats legacy: got %q, want %q", got, dotDir)
	}
	top := write("layers.toml")
	if got := findLayersConfig(root); got != top {
		t.Errorf("root layers.toml first, as Cartographer reads it: got %q, want %q", got, top)
	}
}
