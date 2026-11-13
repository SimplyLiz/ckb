package mcp

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/index"
)

// IndexDiagnosis is what building the SCIP index for a repo would run into
// right now, checked against this machine (language detected, indexer on
// PATH, repo size), not guessed.
type IndexDiagnosis struct {
	// Ready means the reindex tool can build the index as things stand.
	Ready bool
	// Problem says what stops the build, with the exact command that fixes it.
	// Empty when Ready.
	Problem string
	// ThenReindex means the reindex tool can build the index once Problem is
	// fixed. False when the build has to run in a terminal ('ckb index --lang',
	// 'ckb index --scip') or can't happen at all (no indexable language).
	ThenReindex bool
}

// IndexDiagnoser checks what an index build for repoRoot would run into. The
// language detection and indexer plan live in cmd/ckb, so the binary injects
// one with SetIndexDiagnoser, like the ReindexRunner.
type IndexDiagnoser func(repoRoot string) IndexDiagnosis

// SetIndexDiagnoser installs the function the missing-index warning uses to
// name the next step. Without one the warning falls back to 'ckb index'.
func (s *MCPServer) SetIndexDiagnoser(d IndexDiagnoser) {
	s.indexDiagnoser = d
}

// indexDiagnosisTTL bounds how often the diagnoser walks the repo: an agent
// that installs the indexer sees the warning change within a minute.
const indexDiagnosisTTL = time.Minute

type missingIndexCache struct {
	mu      sync.Mutex
	repo    string
	at      time.Time
	message string
}

// missingIndexText is the start of every missing-index warning.
const missingIndexText = "No SCIP index for this repo, so symbols, references, callers and impact " +
	"come from text search and parsers and can miss uses. "

// missingIndexWarnings returns a warning for a response served without a
// loaded SCIP index, naming the next step that was checked for this repo.
// Nothing when the index is loaded, when SCIP is off for this engine, or for
// the reindex tool, which reports the build itself.
//
// Without it an agent, or a user who never ran 'ckb index', gets thinner
// answers with no sign of why: the first-index build that 'ckb mcp' starts
// fails quietly when the indexer isn't installed.
func (s *MCPServer) missingIndexWarnings(toolName string) []envelope.Warning {
	if toolName == "reindex" {
		return nil
	}
	eng := s.loadedEngine()
	if eng == nil {
		return nil
	}
	sb := eng.GetScipBackend()
	if sb == nil || sb.IsAvailable() {
		return nil
	}
	repoRoot := eng.GetRepoRoot()
	if repoRoot == "" {
		return nil
	}

	if index.BuildRunning(filepath.Join(repoRoot, ".ckb")) {
		return []envelope.Warning{{
			Code: "index_building",
			Message: "No SCIP index yet; one is being built right now. Until it is loaded, " +
				"symbols, references, callers and impact come from text search and parsers and can miss uses. " +
				"This server loads it on the first call after the build finishes.",
		}}
	}

	c := &s.missingIndex
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.repo != repoRoot || time.Since(c.at) > indexDiagnosisTTL {
		c.repo, c.at, c.message = repoRoot, time.Now(), s.missingIndexMessage(repoRoot)
	}
	return []envelope.Warning{{Code: "index_missing", Message: c.message}}
}

// missingIndexMessage builds the warning text from the diagnosis.
func (s *MCPServer) missingIndexMessage(repoRoot string) string {
	canReindex := s.reindexRunner != nil && s.toolActive("reindex")
	reindexStep := "Call the reindex tool to build it."
	if !canReindex {
		reindexStep = "Run 'ckb index' in a terminal; this server loads the index on its next call."
	}

	if s.indexDiagnoser == nil {
		return missingIndexText + reindexStep
	}
	d := s.indexDiagnoser(repoRoot)
	switch {
	case d.Ready:
		return missingIndexText + reindexStep
	case d.ThenReindex && canReindex:
		return missingIndexText + d.Problem + " Then call the reindex tool."
	case d.ThenReindex:
		return missingIndexText + d.Problem + " Then run 'ckb index'; this server loads the index on its next call."
	default:
		return missingIndexText + d.Problem
	}
}

// toolActive reports whether name is in this session's tool list.
func (s *MCPServer) toolActive(name string) bool {
	for _, t := range s.GetFilteredTools() {
		if t.Name == name {
			return true
		}
	}
	return false
}
