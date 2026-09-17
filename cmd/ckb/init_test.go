package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/repos"
)

// chdirTemp changes to a fresh temp dir for the duration of the test and
// restores the original cwd afterward. The returned path is symlink-
// resolved (matching what os.Getwd() returns) so callers can compare it
// against registry entries without tripping over macOS's /var ->
// /private/var symlink.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	return dir
}

// TestRunInitCore_ActivatesByDefault verifies plain 'ckb init' behavior is
// unchanged: with NoActivate: false, the new repo becomes the global
// default.
func TestRunInitCore_ActivatesByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := chdirTemp(t)

	if err := runInitCore(initOptions{}); err != nil {
		t.Fatalf("runInitCore failed: %v", err)
	}

	registry, err := repos.LoadRegistry()
	if err != nil {
		t.Fatalf("failed to load registry: %v", err)
	}
	if registry.Default == "" {
		t.Error("expected a default repo to be set")
	}
	entry, ok := registry.Repos[registry.Default]
	if !ok {
		t.Fatalf("default repo %q not found in registry", registry.Default)
	}
	if entry.Path != dir {
		t.Errorf("default repo path = %q, want %q", entry.Path, dir)
	}
}

// TestRunInitCore_NoActivate_DoesNotChangeDefault is the core of the P2
// fix: initializing a second project with NoActivate: true must register
// it (so 'ckb repo list' etc. see it) without touching the registry's
// existing default — a session working on repo A must not have its default
// silently swapped to repo B just because someone ran 'ckb setup' there.
func TestRunInitCore_NoActivate_DoesNotChangeDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Establish an existing default repo (repo A).
	dirA := chdirTemp(t)
	if err := runInitCore(initOptions{}); err != nil {
		t.Fatalf("runInitCore for repo A failed: %v", err)
	}
	registryAfterA, err := repos.LoadRegistry()
	if err != nil {
		t.Fatalf("failed to load registry: %v", err)
	}
	originalDefault := registryAfterA.Default
	if originalDefault == "" {
		t.Fatal("expected repo A to become the default")
	}

	// Now init a second project (repo B) with NoActivate: true — this is
	// what ensureCkbInitialized does on behalf of 'ckb setup'.
	dirB := chdirTemp(t)
	if err := runInitCore(initOptions{NoActivate: true}); err != nil {
		t.Fatalf("runInitCore for repo B failed: %v", err)
	}

	registryAfterB, err := repos.LoadRegistry()
	if err != nil {
		t.Fatalf("failed to reload registry: %v", err)
	}

	if registryAfterB.Default != originalDefault {
		t.Errorf("global default changed from %q to %q — NoActivate should have prevented this",
			originalDefault, registryAfterB.Default)
	}

	// Repo B must still be registered, just not made active.
	entryB, err := registryAfterB.GetByPath(dirB)
	if err != nil {
		t.Fatalf("repo B was not registered: %v", err)
	}
	if entryB == nil {
		t.Fatal("repo B entry is nil")
	}

	// Sanity: repo A's path is still the resolved default.
	defaultEntry, ok := registryAfterB.Repos[registryAfterB.Default]
	if !ok || defaultEntry.Path != dirA {
		t.Errorf("default entry path = %+v, want repo A's path %q", defaultEntry, dirA)
	}
}

// TestEnsureCkbInitialized_DoesNotChangeGlobalDefault is the integration-
// level version of the same guarantee, exercised through the actual
// 'ckb setup' code path rather than calling runInitCore directly.
func TestEnsureCkbInitialized_DoesNotChangeGlobalDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	chdirTemp(t) // repo A
	if err := runInitCore(initOptions{}); err != nil {
		t.Fatalf("runInitCore for repo A failed: %v", err)
	}
	registryAfterA, err := repos.LoadRegistry()
	if err != nil {
		t.Fatalf("failed to load registry: %v", err)
	}
	originalDefault := registryAfterA.Default

	chdirTemp(t) // repo B, uninitialized
	if err := ensureCkbInitialized(); err != nil {
		t.Fatalf("ensureCkbInitialized failed: %v", err)
	}

	registryAfterB, err := repos.LoadRegistry()
	if err != nil {
		t.Fatalf("failed to reload registry: %v", err)
	}
	if registryAfterB.Default != originalDefault {
		t.Errorf("ensureCkbInitialized changed the global default from %q to %q",
			originalDefault, registryAfterB.Default)
	}
}

// TestEnsureCkbInitialized_Idempotent verifies the existing short-circuit
// (skip entirely if .ckb/ already exists) still works after the refactor.
func TestEnsureCkbInitialized_Idempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := chdirTemp(t)

	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0755); err != nil {
		t.Fatalf("failed to pre-create .ckb: %v", err)
	}

	if err := ensureCkbInitialized(); err != nil {
		t.Fatalf("ensureCkbInitialized failed: %v", err)
	}

	// No config.json should have been written by us since .ckb/ already
	// existed — the function should have returned immediately.
	if _, err := os.Stat(filepath.Join(dir, ".ckb", "config.json")); err == nil {
		t.Error("expected no config.json to be written when .ckb/ already existed")
	}
}
