package mcp

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/index"
)

func noopRunner(context.Context, string, ReindexScope) (*ReindexRunInfo, error) {
	return &ReindexRunInfo{Mode: "full"}, nil
}

func onlyWarning(t *testing.T, s *MCPServer, tool string) string {
	t.Helper()
	ws := s.missingIndexWarnings(tool)
	if len(ws) != 1 {
		t.Fatalf("%s: want one warning, got %d: %+v", tool, len(ws), ws)
	}
	return ws[0].Code + ": " + ws[0].Message
}

func TestMissingIndexWarning_NoneWithIndexLoaded(t *testing.T) {
	s, _ := newReindexTestServer(t, true)
	if ws := s.missingIndexWarnings("findReferences"); len(ws) != 0 {
		t.Fatalf("loaded index still warned: %+v", ws)
	}
}

func TestMissingIndexWarning_ReindexToolIsLeftAlone(t *testing.T) {
	s, _ := newReindexTestServer(t, false)
	if ws := s.missingIndexWarnings("reindex"); len(ws) != 0 {
		t.Fatalf("reindex got the warning: %+v", ws)
	}
}

func TestMissingIndexWarning_ReadyPointsAtReindex(t *testing.T) {
	s, _ := newReindexTestServer(t, false)
	s.SetReindexRunner(noopRunner)
	s.SetIndexDiagnoser(func(string) IndexDiagnosis { return IndexDiagnosis{Ready: true} })

	got := onlyWarning(t, s, "findReferences")
	if !strings.HasPrefix(got, "index_missing: No SCIP index") || !strings.Contains(got, "Call the reindex tool") {
		t.Fatalf("warning = %q", got)
	}
}

func TestMissingIndexWarning_MissingIndexerNamesInstallThenReindex(t *testing.T) {
	s, _ := newReindexTestServer(t, false)
	s.SetReindexRunner(noopRunner)
	s.SetIndexDiagnoser(func(string) IndexDiagnosis {
		return IndexDiagnosis{
			Problem:     "Indexing Python needs this first: scip-python not installed (install with: npm install -g @sourcegraph/scip-python).",
			ThenReindex: true,
		}
	})

	got := onlyWarning(t, s, "analyzeImpact")
	for _, want := range []string{"npm install -g @sourcegraph/scip-python", "Then call the reindex tool."} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning lacks %q: %q", want, got)
		}
	}
}

func TestMissingIndexWarning_TerminalOnlyProblemDoesNotSayReindex(t *testing.T) {
	s, _ := newReindexTestServer(t, false)
	s.SetReindexRunner(noopRunner)
	s.SetIndexDiagnoser(func(string) IndexDiagnosis {
		return IndexDiagnosis{Problem: "This repo has several languages (python, typescript) and CKB indexes one; run 'ckb index --lang <language>'."}
	})

	got := onlyWarning(t, s, "searchSymbols")
	if strings.Contains(got, "reindex") {
		t.Fatalf("terminal-only problem points at reindex: %q", got)
	}
	if !strings.Contains(got, "ckb index --lang") {
		t.Fatalf("warning = %q", got)
	}
}

func TestMissingIndexWarning_NoRunnerFallsBackToTerminal(t *testing.T) {
	s, _ := newReindexTestServer(t, false)

	got := onlyWarning(t, s, "explore")
	if strings.Contains(got, "reindex tool") || !strings.Contains(got, "Run 'ckb index' in a terminal") {
		t.Fatalf("warning = %q", got)
	}
}

func TestMissingIndexWarning_ReindexNotInPresetFallsBackToTerminal(t *testing.T) {
	s, _ := newReindexTestServer(t, false)
	s.SetReindexRunner(noopRunner)
	s.SetIndexDiagnoser(func(string) IndexDiagnosis { return IndexDiagnosis{Ready: true} })
	if err := s.SetPreset(PresetFederation); err != nil {
		t.Fatal(err)
	}
	// Federation carries reindex since it joined the core list; take it out to
	// stand for a preset without it.
	saved := Presets[PresetFederation]
	t.Cleanup(func() { Presets[PresetFederation] = saved })
	var without []string
	for _, n := range saved {
		if n != "reindex" {
			without = append(without, n)
		}
	}
	Presets[PresetFederation] = without

	got := onlyWarning(t, s, "explore")
	if strings.Contains(got, "reindex tool") {
		t.Fatalf("points at a tool this session doesn't have: %q", got)
	}
}

func TestMissingIndexWarning_BuildRunning(t *testing.T) {
	s, dir := newReindexTestServer(t, false)
	lock, err := index.AcquireLock(filepath.Join(dir, ".ckb"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	got := onlyWarning(t, s, "findReferences")
	if !strings.HasPrefix(got, "index_building:") {
		t.Fatalf("warning = %q", got)
	}
}

func TestMissingIndexWarning_DiagnosisIsCached(t *testing.T) {
	s, _ := newReindexTestServer(t, false)
	calls := 0
	s.SetIndexDiagnoser(func(string) IndexDiagnosis { calls++; return IndexDiagnosis{Ready: true} })

	onlyWarning(t, s, "findReferences")
	onlyWarning(t, s, "getCallGraph")
	if calls != 1 {
		t.Fatalf("diagnoser ran %d times for two calls", calls)
	}
}

// A streamed answer comes from the same index as a plain one, so its initial
// response carries the same index_missing warning.
func TestMissingIndexWarning_OnStreamingInitialResponse(t *testing.T) {
	s, _ := newReindexTestServer(t, false)
	s.SetStdout(io.Discard) // the stream's chunks go out as notifications
	s.SetIndexDiagnoser(func(string) IndexDiagnosis { return IndexDiagnosis{Ready: true} })

	res, err := s.handleCallTool(map[string]interface{}{
		"name":      "searchSymbols",
		"arguments": map[string]interface{}{"query": "Engine", "stream": true},
	})
	if err != nil {
		t.Fatalf("searchSymbols: %v", err)
	}
	text := res.(map[string]interface{})["content"].([]map[string]interface{})[0]["text"].(string)
	if !strings.Contains(text, `"streaming":true`) {
		t.Fatalf("not a streaming initial response: %s", text)
	}
	if !strings.Contains(text, "index_missing") {
		t.Fatalf("streaming initial response has no index_missing warning: %s", text)
	}
}
