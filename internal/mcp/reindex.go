package mcp

import (
	"context"
	"errors"
	"sync"
)

// ReindexScope is what the reindex tool asks the runner to do.
type ReindexScope string

const (
	// ReindexAuto uses incremental indexing where the language supports it
	// and falls back to a full build.
	ReindexAuto ReindexScope = "auto"
	// ReindexFull always rebuilds the whole index.
	ReindexFull ReindexScope = "full"
	// ReindexIncremental is ReindexAuto today; it exists so clients can say
	// what they want once a runner can refuse a full build.
	ReindexIncremental ReindexScope = "incremental"
)

// ReindexRunInfo is what a ReindexRunner reports about a successful build.
type ReindexRunInfo struct {
	// Mode is "full" or "incremental".
	Mode string
}

// ReindexRunner builds the SCIP index for repoRoot in-process and returns how
// it was built. The MCP server does not know how to run an indexer — the
// language detection and indexer plan live in cmd/ckb — so the binary injects
// one with SetReindexRunner. Without a runner (tests, library embedding) the
// reindex tool falls back to telling the caller to run 'ckb index'.
//
// Errors wrapping ErrReindexNotPossible mean "this repo cannot be indexed from
// here" (no indexer installed, repo above the automatic size threshold) and
// are reported as action_required, not as a failure. ErrReindexLocked means
// another process holds the index lock.
type ReindexRunner func(ctx context.Context, repoRoot string, scope ReindexScope) (*ReindexRunInfo, error)

var (
	// ErrReindexNotPossible marks runner errors that need the user, not a retry.
	ErrReindexNotPossible = errors.New("reindex not possible from the MCP server")
	// ErrReindexLocked means another process is indexing this repo right now.
	ErrReindexLocked = errors.New("index lock held by another process")
)

// SetReindexRunner installs the function the reindex tool uses to build an
// index. Call before serving.
func (s *MCPServer) SetReindexRunner(r ReindexRunner) {
	s.reindexRunner = r
}

// reindexGuard allows one reindex per repository at a time within this
// process; a second call while one is running gets status "busy" instead of
// queueing behind a multi-minute build.
type reindexGuard struct {
	mu      sync.Mutex
	running map[string]bool
}

func (g *reindexGuard) tryBegin(repoRoot string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running == nil {
		g.running = make(map[string]bool)
	}
	if g.running[repoRoot] {
		return false
	}
	g.running[repoRoot] = true
	return true
}

func (g *reindexGuard) end(repoRoot string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.running, repoRoot)
}
