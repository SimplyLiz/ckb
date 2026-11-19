package mcp

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/repos"
)

// toolListRepos lists all registered repositories and loaded engines
func (s *MCPServer) toolListRepos(params map[string]interface{}) (*envelope.Response, error) {
	s.logger.Debug("Executing listRepos")

	type repoInfo struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		State     string `json:"state"`
		IsDefault bool   `json:"is_default"`
		IsActive  bool   `json:"is_active"`
		IsLoaded  bool   `json:"is_loaded"`
	}

	activeRepo, _ := s.GetActiveRepo()
	var repoList []repoInfo
	var defaultName string

	// Include repos from registry if available
	registry, err := repos.LoadRegistry()
	if err == nil && len(registry.List()) > 0 {
		defaultName = registry.Default
		for _, entry := range registry.List() {
			state := registry.ValidateState(entry.Name)

			s.mu.RLock()
			_, isLoaded := s.engines[entry.Path]
			s.mu.RUnlock()

			repoList = append(repoList, repoInfo{
				Name:      entry.Name,
				Path:      entry.Path,
				State:     string(state),
				IsDefault: entry.Name == registry.Default,
				IsActive:  entry.Name == activeRepo,
				IsLoaded:  isLoaded,
			})
		}
	}

	// Also include any loaded engines not in the registry
	s.mu.RLock()
	for path, entry := range s.engines {
		found := false
		for _, r := range repoList {
			if r.Path == path {
				found = true
				break
			}
		}
		if !found {
			repoList = append(repoList, repoInfo{
				Name:     entry.repoName,
				Path:     entry.repoPath,
				State:    "valid",
				IsActive: entry.repoPath == s.activeRepoPath,
				IsLoaded: true,
			})
		}
	}
	s.mu.RUnlock()

	return OperationalResponse(map[string]interface{}{
		"repos":      repoList,
		"activeRepo": activeRepo,
		"default":    defaultName,
	}), nil
}

// toolSwitchRepo switches to a different repository
func (s *MCPServer) toolSwitchRepo(params map[string]interface{}) (*envelope.Response, error) {
	s.logger.Debug("Executing switchRepo",
		"params", params,
	)

	name, ok := params["name"].(string)
	if !ok || name == "" {
		return nil, &MCPError{
			Code:    InvalidParams,
			Message: "name parameter is required",
		}
	}

	// Try registry first
	registry, err := repos.LoadRegistry()
	if err == nil {
		entry, state, getErr := registry.Get(name)
		if getErr == nil {
			switch state {
			case repos.RepoStateMissing:
				return nil, &MCPError{
					Code:    InvalidParams,
					Message: fmt.Sprintf("Path does not exist: %s", entry.Path),
					Data:    map[string]string{"hint": fmt.Sprintf("Run: ckb repo remove %s", name)},
				}
			case repos.RepoStateUninitialized:
				return nil, &MCPError{
					Code:    InvalidParams,
					Message: fmt.Sprintf("Repository not initialized: %s", entry.Path),
					Data:    map[string]string{"hint": fmt.Sprintf("Run: cd %s && ckb init", entry.Path)},
				}
			}

			// Use ensureActiveEngine for the switch
			if switchErr := s.ensureActiveEngine(entry.Path); switchErr != nil {
				return nil, errors.NewOperationError("switch to "+name, switchErr)
			}

			// Update the repo name (ensureActiveEngine uses filepath.Base)
			s.mu.Lock()
			s.activeRepo = name
			s.mu.Unlock()

			_ = registry.TouchLastUsed(name)

			return OperationalResponse(map[string]interface{}{
				"success":    true,
				"activeRepo": name,
				"path":       entry.Path,
			}), nil
		}
	}

	// Not in registry — treat name as a path
	return nil, &MCPError{
		Code:    InvalidParams,
		Message: fmt.Sprintf("Repository not found: %s", name),
	}
}

// toolGetActiveRepo returns information about the currently active repository
func (s *MCPServer) toolGetActiveRepo(params map[string]interface{}) (*envelope.Response, error) {
	s.logger.Debug("Executing getActiveRepo")

	name, path := s.GetActiveRepo()

	// Fall back to current engine info if no explicit active repo
	if name == "" && path == "" {
		if eng := s.engine(); eng != nil {
			path = eng.GetRepoRoot()
			name = filepath.Base(path)
		}
	}

	if name == "" {
		return OperationalResponse(map[string]interface{}{
			"name":  nil,
			"state": "none",
			"error": "No active repository. Call switchRepo first or set a default.",
		}), nil
	}

	// Try to get state from registry
	state := "valid"
	if registry, err := repos.LoadRegistry(); err == nil {
		if rs := registry.ValidateState(name); rs != "" {
			state = string(rs)
		}
	}

	return OperationalResponse(map[string]interface{}{
		"name":  name,
		"path":  path,
		"state": state,
	}), nil
}

// evictLRULocked evicts the least recently used engine (must be called with mu held)
func (s *MCPServer) evictLRULocked() {
	var victim string
	var oldest time.Time

	for path, entry := range s.engines {
		// Never evict active repo
		if path == s.activeRepoPath {
			continue
		}
		if victim == "" || entry.lastUsed.Before(oldest) {
			victim = path
			oldest = entry.lastUsed
		}
	}

	if victim != "" {
		entry := s.engines[victim]
		s.logger.Info("Evicting LRU engine",
			"repo", entry.repoName,
			"path", entry.repoPath,
			"lastUsed", entry.lastUsed,
		)
		// Wait for any in-flight operations
		entry.activeOps.Wait()
		// Flush and drop the activity recorder before its DB goes away
		if entry.engine != nil {
			closeActivityRecorder(entry.engine.DB())
			_ = entry.engine.Close()
		}
		delete(s.engines, victim)
	}
}

// CloseAllEngines closes all loaded engines (for graceful shutdown)
func (s *MCPServer) CloseAllEngines() {
	s.mu.Lock()
	entries := make([]*engineEntry, 0, len(s.engines))
	for _, entry := range s.engines {
		entries = append(entries, entry)
	}
	s.engines = make(map[string]*engineEntry)
	s.mu.Unlock()

	// Close outside lock
	for _, entry := range entries {
		entry.activeOps.Wait()
		if entry.engine != nil {
			closeActivityRecorder(entry.engine.DB())
			_ = entry.engine.Close()
		}
	}
}
