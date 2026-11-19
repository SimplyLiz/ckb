package mcp

import (
	"path/filepath"
	"sync"
	"time"
)

// getOrCreateEngine returns a cached engine for the given repo root, creating one if needed.
// Thread-safe: uses s.mu for synchronization.
func (s *MCPServer) getOrCreateEngine(repoRoot string) (*engineEntry, error) {
	normalized := normalizePath(repoRoot)

	// Fast path: check cache with read lock
	s.mu.RLock()
	if entry, ok := s.engines[normalized]; ok {
		entry.lastUsed = time.Now()
		s.mu.RUnlock()
		return entry, nil
	}
	s.mu.RUnlock()

	// Slow path: upgrade to write lock, double-check, create
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, ok := s.engines[normalized]; ok {
		entry.lastUsed = time.Now()
		return entry, nil
	}

	// Evict LRU if at capacity
	if len(s.engines) >= maxEngines {
		s.evictLRULocked()
	}

	// Create new engine
	engine, err := s.createEngineForRoot(normalized)
	if err != nil {
		return nil, err
	}

	entry := &engineEntry{
		engine:   engine,
		repoPath: normalized,
		repoName: filepath.Base(normalized),
		loadedAt: time.Now(),
		lastUsed: time.Now(),
	}
	s.engines[normalized] = entry

	s.logger.Info("Created engine for repo",
		"path", normalized,
		"totalLoaded", len(s.engines),
	)

	return entry, nil
}

// ensureActiveEngine switches the active engine to the repo at repoRoot, if needed.
// No-op if repoRoot is empty or already the active repo.
// MCP over stdio is sequential, so no race on legacyEngine.
func (s *MCPServer) ensureActiveEngine(repoRoot string) error {
	if repoRoot == "" {
		return nil
	}

	normalized := normalizePath(repoRoot)

	// Check if current engine already points here
	if eng := s.engine(); eng != nil {
		currentRoot := normalizePath(eng.GetRepoRoot())
		if currentRoot == normalized {
			return nil
		}
	}

	entry, err := s.getOrCreateEngine(normalized)
	if err != nil {
		s.logger.Warn("Auto-resolve failed, keeping current engine",
			"targetRoot", normalized,
			"error", err.Error(),
		)
		return err
	}

	// Swap the active engine pointer
	s.mu.Lock()
	s.legacyEngine = entry.engine
	s.activeRepo = entry.repoName
	s.activeRepoPath = entry.repoPath
	s.engineOnce = sync.Once{} // mark as loaded
	s.engineErr = nil
	s.mu.Unlock()

	// Wire up metrics persistence
	if entry.engine.DB() != nil {
		SetMetricsDB(entry.engine.DB())
		wireActivityRecorder(entry.engine, s.logger)
	}

	s.logger.Info("Auto-resolved active repo",
		"repo", entry.repoName,
		"path", entry.repoPath,
	)

	return nil
}

// normalizePath cleans and resolves symlinks for a path.
// Always returns a usable path — falls back to filepath.Clean if symlink resolution fails.
func normalizePath(path string) string {
	cleaned := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return cleaned
	}
	return resolved
}
