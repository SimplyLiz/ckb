package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// newReindexTestServer builds an MCP server over a non-git temp repo with the
// SCIP backend enabled. withIndex writes the Go fixture index plus fresh
// metadata before the engine starts, so the engine loads it.
func newReindexTestServer(t *testing.T, withIndex bool) (*MCPServer, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0755); err != nil {
		t.Fatal(err)
	}
	if withIndex {
		writeFixtureIndex(t, dir)
	}
	cfg := config.DefaultConfig()
	cfg.RepoRoot = dir
	cfg.Backends.Git.Enabled = false
	cfg.Backends.Lsp.Enabled = false
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := storage.Open(dir, logger)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	engine, err := query.NewEngine(dir, db, logger, cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return NewMCPServer(version.Version, engine, logger), dir
}

// writeFixtureIndex puts the Go fixture index at <dir>/index.scip and records
// fresh metadata for it, the way 'ckb index' would.
func writeFixtureIndex(t *testing.T, dir string) {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "go", ".scip", "index.scip"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), fixture, 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	meta := &index.IndexMeta{CreatedAt: time.Now(), Indexer: "fixture"}
	if err := meta.Save(filepath.Join(dir, ".ckb")); err != nil {
		t.Fatalf("save meta: %v", err)
	}
}

func callReindex(t *testing.T, s *MCPServer, params map[string]interface{}) *ReindexOutput {
	t.Helper()
	resp, err := s.toolReindex(params)
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	out, ok := resp.Data.(*ReindexOutput)
	if !ok {
		t.Fatalf("reindex data has type %T", resp.Data)
	}
	return out
}

func TestReindex_NoRunner_IsActionRequired(t *testing.T) {
	s, _ := newReindexTestServer(t, false)
	out := callReindex(t, s, nil)
	if out.Status != "action_required" {
		t.Fatalf("status = %q, want action_required (%s)", out.Status, out.Message)
	}
}

func TestReindex_RunsRunnerAndLoadsResult(t *testing.T) {
	s, dir := newReindexTestServer(t, false)
	var gotScope ReindexScope
	s.SetReindexRunner(func(ctx context.Context, repoRoot string, scope ReindexScope) (*ReindexRunInfo, error) {
		gotScope = scope
		if repoRoot != dir {
			t.Errorf("runner repoRoot = %q, want %q", repoRoot, dir)
		}
		writeFixtureIndex(t, repoRoot)
		return &ReindexRunInfo{Mode: "full"}, nil
	})

	if s.engine().GetScipBackend().IsAvailable() {
		t.Fatal("SCIP available before any index exists")
	}
	out := callReindex(t, s, map[string]interface{}{"scope": "auto"})
	if out.Status != "reindexed" || out.Mode != "full" {
		t.Fatalf("status/mode = %q/%q, want reindexed/full (%s)", out.Status, out.Mode, out.Message)
	}
	if gotScope != ReindexAuto {
		t.Errorf("runner scope = %q, want auto", gotScope)
	}
	if out.Symbols == 0 || out.Documents == 0 {
		t.Errorf("counts not reported: %+v", out)
	}
	if !s.engine().GetScipBackend().IsAvailable() {
		t.Fatal("engine did not load the rebuilt index")
	}

	// Fresh and loaded now: auto skips, full rebuilds.
	if out := callReindex(t, s, nil); out.Status != "skipped" {
		t.Fatalf("second auto reindex status = %q, want skipped (%s)", out.Status, out.Message)
	}
	if out := callReindex(t, s, map[string]interface{}{"scope": "full"}); out.Status != "reindexed" {
		t.Fatalf("full reindex status = %q, want reindexed (%s)", out.Status, out.Message)
	}
}

// Index built outside while the server ran, metadata fresh: nothing to
// rebuild, but the engine had not loaded it — that is "reloaded".
func TestReindex_FreshOnDiskNotLoaded_IsReloaded(t *testing.T) {
	s, dir := newReindexTestServer(t, false)
	runnerCalled := false
	s.SetReindexRunner(func(context.Context, string, ReindexScope) (*ReindexRunInfo, error) {
		runnerCalled = true
		return &ReindexRunInfo{Mode: "full"}, nil
	})
	writeFixtureIndex(t, dir)

	out := callReindex(t, s, nil)
	if out.Status != "reloaded" {
		t.Fatalf("status = %q, want reloaded (%s)", out.Status, out.Message)
	}
	if runnerCalled {
		t.Error("runner called although the index on disk was fresh")
	}
	if out.Symbols == 0 {
		t.Errorf("counts not reported after reload: %+v", out)
	}
}

func TestReindex_RunnerSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"not possible", errors.Join(ErrReindexNotPossible, errors.New("no indexer")), "action_required"},
		{"locked", ErrReindexLocked, "busy"},
		{"other", errors.New("indexer exited 1"), "failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newReindexTestServer(t, false)
			s.SetReindexRunner(func(context.Context, string, ReindexScope) (*ReindexRunInfo, error) {
				return nil, c.err
			})
			out := callReindex(t, s, nil)
			if out.Status != c.want {
				t.Fatalf("status = %q, want %q (%s)", out.Status, c.want, out.Message)
			}
			if out.Message == "" {
				t.Error("message empty")
			}
		})
	}
}

func TestReindex_OneRunPerRepo(t *testing.T) {
	s, dir := newReindexTestServer(t, false)
	started := make(chan struct{})
	release := make(chan struct{})
	s.SetReindexRunner(func(ctx context.Context, repoRoot string, scope ReindexScope) (*ReindexRunInfo, error) {
		close(started)
		<-release
		writeFixtureIndex(t, repoRoot)
		return &ReindexRunInfo{Mode: "full"}, nil
	})

	var wg sync.WaitGroup
	wg.Add(1)
	var first *ReindexOutput
	go func() {
		defer wg.Done()
		first = callReindex(t, s, nil)
	}()
	<-started

	second := callReindex(t, s, nil)
	if second.Status != "busy" {
		t.Fatalf("concurrent reindex status = %q, want busy (%s)", second.Status, second.Message)
	}
	close(release)
	wg.Wait()
	if first.Status != "reindexed" {
		t.Fatalf("first reindex status = %q, want reindexed (%s)", first.Status, first.Message)
	}
	_ = dir
}

func TestReindex_InvalidScope(t *testing.T) {
	s, _ := newReindexTestServer(t, false)
	if _, err := s.toolReindex(map[string]interface{}{"scope": "bogus"}); err == nil {
		t.Fatal("bogus scope accepted")
	}
}

func TestReindex_InReviewAndRefactorPresets(t *testing.T) {
	for _, preset := range []string{PresetReview, PresetRefactor, PresetOps} {
		found := false
		for _, name := range GetPresetTools(preset) {
			if name == "reindex" {
				found = true
			}
		}
		if !found {
			t.Errorf("reindex missing from preset %s", preset)
		}
	}
}
