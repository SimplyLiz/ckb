package mcp

import (
	"bufio"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/repos"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

const maxEngines = 5

// engineEntry holds an engine and its metadata
type engineEntry struct {
	engine    *query.Engine
	repoPath  string
	repoName  string
	loadedAt  time.Time
	lastUsed  time.Time
	activeOps sync.WaitGroup
}

// MCPServer represents the MCP server
type MCPServer struct {
	stdin     io.Reader
	stdout    io.Writer
	scanner   *bufio.Scanner
	logger    *slog.Logger
	version   string
	tools     map[string]ToolHandler
	resources map[string]ResourceHandler

	// Legacy single-engine mode
	legacyEngine *query.Engine

	// Multi-repo mode
	engines        map[string]*engineEntry // keyed by normalized path
	activeRepo     string                  // current repo name
	activeRepoPath string                  // current repo path
	registry       *repos.Registry
	mu             sync.RWMutex

	// Preset configuration (for tools/list pagination)
	activePreset string // current preset (core, review, refactor, etc.)
	toolsetHash  string // hash of current tool definitions (for cursor invalidation)
	expanded     bool   // true if expandToolset has been called this session

	// MCP roots support (v8.0)
	roots *rootsManager

	// Client identity captured from the initialize handshake (activity ledger consumer field)
	clientName    string
	clientVersion string

	// Binary staleness detection (v8.0)
	binaryPath    string    // Path to the running binary
	binaryModTime time.Time // Modification time at startup

	// Lazy engine loading (for fast MCP startup)
	engineLoader func() (*query.Engine, error)
	engineOnce   sync.Once
	engineErr    error
}

// NewMCPServer creates a new MCP server in legacy single-engine mode
func NewMCPServer(version string, engine *query.Engine, logger *slog.Logger) *MCPServer {
	server := &MCPServer{
		stdin:        os.Stdin,
		stdout:       os.Stdout,
		logger:       logger,
		version:      version,
		legacyEngine: engine,
		engines:      make(map[string]*engineEntry),
		tools:        make(map[string]ToolHandler),
		resources:    make(map[string]ResourceHandler),
		activePreset: DefaultPreset,
		roots:        newRootsManager(),
	}

	// Record binary info for staleness detection
	server.recordBinaryInfo()

	// Register all tools
	server.RegisterTools()

	// Compute initial toolset hash
	server.updateToolsetHash()

	// Wire up metrics persistence if database is available
	if engine != nil && engine.DB() != nil {
		SetMetricsDB(engine.DB())
		wireActivityRecorder(engine, server.logger)
	}

	// Warm up the FTS index in the background. This populates FTS from SCIP
	// if needed, so the first searchSymbols/listSymbols call doesn't hit empty FTS.
	// We call RefreshFTS instead of SearchSymbols to avoid caching empty results
	// that would mask SCIP data loaded after warmup.
	if engine != nil {
		go func() {
			warmCtx, warmCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer warmCancel()
			_ = engine.RefreshFTS(warmCtx)
		}()
	}

	// Store initial engine in cache for auto-resolution
	if engine != nil {
		repoRoot := engine.GetRepoRoot()
		normalized := normalizePath(repoRoot)
		if normalized != "" {
			server.engines[normalized] = &engineEntry{
				engine:   engine,
				repoPath: normalized,
				repoName: filepath.Base(normalized),
				loadedAt: time.Now(),
				lastUsed: time.Now(),
			}
			server.activeRepoPath = normalized
			server.activeRepo = filepath.Base(normalized)
		}
	}

	return server
}

// NewMCPServerForCLI creates a minimal MCP server for CLI tool introspection.
// This server cannot handle tool calls but can provide tool definitions.
func NewMCPServerForCLI() *MCPServer {
	return &MCPServer{
		activePreset: DefaultPreset,
	}
}

// EngineLoader is a function that creates an engine on demand
type EngineLoader func() (*query.Engine, error)

// NewMCPServerLazy creates a new MCP server with lazy engine loading.
// The engine is not created until the first tool call that needs it.
// This allows the MCP handshake to complete quickly.
func NewMCPServerLazy(version string, loader EngineLoader, logger *slog.Logger) *MCPServer {
	server := &MCPServer{
		stdin:        os.Stdin,
		stdout:       os.Stdout,
		logger:       logger,
		version:      version,
		engineLoader: loader,
		engines:      make(map[string]*engineEntry),
		tools:        make(map[string]ToolHandler),
		resources:    make(map[string]ResourceHandler),
		activePreset: DefaultPreset,
		roots:        newRootsManager(),
	}

	// Record binary info for staleness detection
	server.recordBinaryInfo()

	// Register all tools
	server.RegisterTools()

	// Compute initial toolset hash
	server.updateToolsetHash()

	return server
}

// NewMCPServerWithRegistry creates a new MCP server with multi-repo support
func NewMCPServerWithRegistry(version string, registry *repos.Registry, logger *slog.Logger) *MCPServer {
	server := &MCPServer{
		stdin:        os.Stdin,
		stdout:       os.Stdout,
		logger:       logger,
		version:      version,
		registry:     registry,
		engines:      make(map[string]*engineEntry),
		tools:        make(map[string]ToolHandler),
		resources:    make(map[string]ResourceHandler),
		activePreset: DefaultPreset,
		roots:        newRootsManager(),
	}

	// Record binary info for staleness detection
	server.recordBinaryInfo()

	// Register all tools
	server.RegisterTools()

	// Compute initial toolset hash
	server.updateToolsetHash()

	return server
}

// engine returns the current engine (for backward compatibility with tool handlers)
func (s *MCPServer) engine() *query.Engine {
	// Legacy mode with preloaded engine
	if s.legacyEngine != nil {
		return s.legacyEngine
	}

	// Legacy mode with lazy loading
	if s.engineLoader != nil {
		s.engineOnce.Do(func() {
			s.logger.Info("Loading engine (lazy initialization)...")
			engine, err := s.engineLoader()
			if err != nil {
				s.engineErr = err
				s.logger.Error("Failed to load engine", "error", err.Error())
				return
			}
			s.legacyEngine = engine
			// Wire up metrics persistence
			if engine != nil && engine.DB() != nil {
				SetMetricsDB(engine.DB())
				wireActivityRecorder(engine, s.logger)
			}
			// Store in engine cache for auto-resolution
			if engine != nil {
				repoRoot := engine.GetRepoRoot()
				normalized := normalizePath(repoRoot)
				if normalized != "" {
					s.mu.Lock()
					s.engines[normalized] = &engineEntry{
						engine:   engine,
						repoPath: normalized,
						repoName: filepath.Base(normalized),
						loadedAt: time.Now(),
						lastUsed: time.Now(),
					}
					if s.activeRepoPath == "" {
						s.activeRepoPath = normalized
						s.activeRepo = filepath.Base(normalized)
					}
					s.mu.Unlock()
				}
			}
			s.logger.Info("Engine loaded successfully")
		})
		return s.legacyEngine
	}

	// Multi-repo mode
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.activeRepoPath == "" {
		return nil
	}

	entry, ok := s.engines[s.activeRepoPath]
	if !ok {
		return nil
	}

	return entry.engine
}

// GetEngine returns the current engine or an error if none is active
func (s *MCPServer) GetEngine() (*query.Engine, error) {
	engine := s.engine()
	if engine == nil {
		return nil, errors.NewPreconditionError("no active repository", "Call listRepos to see available repos, then switchRepo")
	}
	return engine, nil
}

// IsMultiRepoMode returns true if the server is in multi-repo mode
func (s *MCPServer) IsMultiRepoMode() bool {
	return s.registry != nil
}

// GetActiveRepo returns the current active repo name and path
func (s *MCPServer) GetActiveRepo() (name string, path string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeRepo, s.activeRepoPath
}

// SetActiveRepo sets the initial active repo (used during startup)
func (s *MCPServer) SetActiveRepo(name, path string, engine *query.Engine) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.activeRepo = name
	s.activeRepoPath = path

	if engine != nil {
		s.engines[path] = &engineEntry{
			engine:   engine,
			repoPath: path,
			repoName: name,
			loadedAt: time.Now(),
			lastUsed: time.Now(),
		}
		// Wire up metrics persistence for multi-repo mode
		if engine.DB() != nil {
			SetMetricsDB(engine.DB())
			wireActivityRecorder(engine, s.logger)
		}
	}
}

// Start starts the MCP server and begins processing messages
func (s *MCPServer) Start() error {
	s.logger.Info("MCP server starting",
		"version", s.version,
	)

	// Main message loop
	for {
		msg, err := s.readMessage()
		if err != nil {
			if err == io.EOF {
				s.logger.Info("MCP server shutting down (EOF)")
				// Cleanup pending roots requests to prevent goroutine leaks
				if s.roots != nil {
					s.roots.CancelAllPending()
				}
				// Flush and stop the activity ledger writer(s) before exiting.
				closeAllActivityRecorders()
				return nil
			}
			s.logger.Error("Error reading message",
				"error", err.Error(),
			)

			// Try to send error response if we can extract an ID
			if msg != nil && msg.Id != nil {
				_ = s.writeError(msg.Id, ParseError, fmt.Sprintf("Failed to parse message: %v", err))
			}
			continue
		}

		// Process the message
		response := s.handleMessage(msg)

		// Write response if one was generated (notifications don't generate responses)
		if response != nil {
			if err := s.writeMessage(response); err != nil {
				s.logger.Error("Error writing response",
					"error", err.Error(),
				)
			}
		}
	}
}

// SetStdin sets the input stream (for testing)
func (s *MCPServer) SetStdin(r io.Reader) {
	s.stdin = r
	s.scanner = nil // Reset scanner so it will be recreated with new reader
}

// SetStdout sets the output stream (for testing)
func (s *MCPServer) SetStdout(w io.Writer) {
	s.stdout = w
}

// SetPreset sets the active preset and updates the toolset hash
func (s *MCPServer) SetPreset(preset string) error {
	if !IsValidPreset(preset) {
		return errors.NewInvalidParameterError("preset", fmt.Sprintf("%s (valid: %v)", preset, ValidPresets()))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.activePreset = preset
	s.updateToolsetHashLocked()

	s.logger.Info("Preset changed",
		"preset", preset,
	)

	return nil
}

// GetActivePreset returns the current active preset
func (s *MCPServer) GetActivePreset() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.activePreset == "" {
		return DefaultPreset
	}
	return s.activePreset
}

// GetToolsetHash returns the current toolset hash (for cursor validation)
func (s *MCPServer) GetToolsetHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.toolsetHash
}

// GetFilteredTools returns tools filtered by the active preset, ordered core-first
func (s *MCPServer) GetFilteredTools() []Tool {
	s.mu.RLock()
	preset := s.activePreset
	s.mu.RUnlock()

	if preset == "" {
		preset = DefaultPreset
	}

	allTools := s.GetToolDefinitions()
	return FilterAndOrderTools(allTools, preset)
}

// GetPresetStats returns statistics about the current preset
func (s *MCPServer) GetPresetStats() (preset string, exposedCount int, totalCount int) {
	preset = s.GetActivePreset()
	allTools := s.GetToolDefinitions()
	filteredTools := s.GetFilteredTools()
	return preset, len(filteredTools), len(allTools)
}

// EstimateActiveTokens returns estimated tokens for the active preset's tools/list response
func (s *MCPServer) EstimateActiveTokens() int {
	tools := s.GetFilteredTools()
	return EstimateTokens(MeasureJSONSize(tools))
}

// EstimateFullTokens returns estimated tokens for the full preset (all tools)
func (s *MCPServer) EstimateFullTokens() int {
	allTools := s.GetToolDefinitions()
	return EstimateTokens(MeasureJSONSize(allTools))
}

// updateToolsetHash recomputes the toolset hash (call with lock held or during init)
func (s *MCPServer) updateToolsetHash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateToolsetHashLocked()
}

// updateToolsetHashLocked recomputes the toolset hash (caller must hold lock)
func (s *MCPServer) updateToolsetHashLocked() {
	allTools := s.GetToolDefinitions()
	filteredTools := FilterAndOrderTools(allTools, s.activePreset)
	s.toolsetHash = ComputeToolsetHash(filteredTools)
}

// IsExpanded returns true if expandToolset has been called this session
func (s *MCPServer) IsExpanded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.expanded
}

// MarkExpanded marks the session as expanded (rate limit: one expansion per session)
func (s *MCPServer) MarkExpanded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expanded = true
}

// CallTool invokes an MCP tool handler by name and returns its envelope response.
// This is the public bridge used by the A2A server to execute CKB tools.
func (s *MCPServer) CallTool(name string, params map[string]interface{}) (*envelope.Response, error) {
	handler, exists := s.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return handler(params)
}

// GetRoots returns the current MCP roots from the client (v8.0)
func (s *MCPServer) GetRoots() []Root {
	if s.roots == nil {
		return nil
	}
	return s.roots.GetRoots()
}

// GetRootPaths returns the filesystem paths for all client roots (v8.0)
func (s *MCPServer) GetRootPaths() []string {
	if s.roots == nil {
		return nil
	}
	return s.roots.GetPaths()
}

// HasClientRoots returns true if the client provided any roots (v8.0)
func (s *MCPServer) HasClientRoots() bool {
	roots := s.GetRoots()
	return len(roots) > 0
}

// createEngineForRoot creates a new query engine for the given root directory
func (s *MCPServer) createEngineForRoot(repoRoot string) (*query.Engine, error) {
	// Load configuration for the new root
	cfg, err := config.LoadConfig(repoRoot)
	if err != nil {
		s.logger.Debug("Failed to load config for root, using defaults",
			"root", repoRoot,
			"error", err.Error(),
		)
		cfg = config.DefaultConfig()
	}

	// Open storage for the new root
	db, err := storage.Open(repoRoot, s.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Create engine
	engine, err := query.NewEngine(repoRoot, db, s.logger, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create engine: %w", err)
	}
	engine.StartBgTasks()

	return engine, nil
}

// switchToClientRoot switches the engine to the client's root directory if different.
// This fixes repo confusion when using a binary from a different location.
// Uses the engine cache so old engines are retained for auto-resolution.
func (s *MCPServer) switchToClientRoot(clientRoot string) {
	if clientRoot == "" {
		return
	}

	clientRootClean := filepath.Clean(clientRoot)

	// Check if current engine already points here
	if eng := s.engine(); eng != nil {
		currentRootClean := filepath.Clean(eng.GetRepoRoot())
		if clientRootClean == currentRootClean {
			s.logger.Debug("Client root matches current repo, no switch needed",
				"root", clientRootClean,
			)
			return
		}
	}

	s.logger.Info("Client root differs from server repo, switching to client's project",
		"clientRoot", clientRootClean,
	)

	// Use ensureActiveEngine which handles caching and swapping
	if err := s.ensureActiveEngine(clientRootClean); err != nil {
		s.logger.Warn("Failed to switch to client root, keeping current repo",
			"clientRoot", clientRootClean,
			"error", err.Error(),
		)
	}
}

// enrichNotFoundError adds repo context to "not found" errors when the client
// didn't provide roots (e.g. Cursor). This prevents AI agents from hallucinating
// explanations for missing symbols/paths that are actually caused by a repo mismatch.
func (s *MCPServer) enrichNotFoundError(err error) error {
	// If client supports roots, auto-switch should have handled it — don't add noise
	if s.roots != nil && s.roots.IsClientSupported() {
		return err
	}

	var ckbErr *errors.CkbError
	if !stderrors.As(err, &ckbErr) {
		return err
	}

	if ckbErr.Code != errors.ResourceNotFound && ckbErr.Code != errors.SymbolNotFound {
		return err
	}

	engine := s.engine()
	if engine == nil {
		return err
	}

	enriched := fmt.Sprintf("%s (note: CKB index is for %s — if your project is in a different directory, call the switchProject tool with the correct path to switch)",
		ckbErr.Message, engine.GetRepoRoot())
	return errors.NewCkbError(ckbErr.Code, enriched, ckbErr.Unwrap(), ckbErr.SuggestedFixes, ckbErr.Drilldowns)
}

// switchProject switches the active engine to a different project directory.
// Works in all modes: single-engine, lazy, and multi-repo.
func (s *MCPServer) switchProject(path string) (string, error) {
	// Validate path exists and is a directory
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("path does not exist: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", path)
	}

	// Find git root
	gitRoot := repos.FindGitRoot(path)
	if gitRoot == "" {
		return "", fmt.Errorf("not a git repository: %s", path)
	}

	// Check if .ckb/ exists (initialized)
	ckbDir := filepath.Join(gitRoot, ".ckb")
	if _, statErr := os.Stat(ckbDir); os.IsNotExist(statErr) {
		return "", fmt.Errorf("CKB not initialized for %s — run 'ckb setup' from that directory first", gitRoot)
	}

	// Check if we're already on this root
	currentEngine := s.engine()
	if currentEngine != nil {
		currentRoot := filepath.Clean(currentEngine.GetRepoRoot())
		if currentRoot == filepath.Clean(gitRoot) {
			return gitRoot, nil // already there
		}
	}

	// Close old engine if any
	if currentEngine != nil && currentEngine.DB() != nil {
		if closeErr := currentEngine.DB().Close(); closeErr != nil {
			s.logger.Warn("Failed to close old engine database", "error", closeErr.Error())
		}
	}

	// Create new engine for the target root
	newEngine, err := s.createEngineForRoot(gitRoot)
	if err != nil {
		return "", fmt.Errorf("failed to create engine for %s: %w", gitRoot, err)
	}

	// Update engine state
	s.mu.Lock()
	s.legacyEngine = newEngine
	s.engineOnce = sync.Once{} // reset for lazy mode
	s.engineErr = nil
	s.mu.Unlock()

	// Wire up metrics persistence
	if newEngine.DB() != nil {
		SetMetricsDB(newEngine.DB())
		wireActivityRecorder(newEngine, s.logger)
	}

	s.logger.Info("Switched project", "root", gitRoot)
	return gitRoot, nil
}

// recordBinaryInfo records the current binary's path and modification time
func (s *MCPServer) recordBinaryInfo() {
	execPath, err := os.Executable()
	if err != nil {
		if s.logger != nil {
			s.logger.Debug("Failed to get executable path", "error", err.Error())
		}
		return
	}

	// Resolve symlinks to get the actual binary
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath
	}

	info, err := os.Stat(realPath)
	if err != nil {
		if s.logger != nil {
			s.logger.Debug("Failed to stat binary", "path", realPath, "error", err.Error())
		}
		return
	}

	s.binaryPath = realPath
	s.binaryModTime = info.ModTime()

	if s.logger != nil {
		s.logger.Debug("Recorded binary info",
			"path", s.binaryPath,
			"modTime", s.binaryModTime.Format(time.RFC3339),
		)
	}
}

// IsBinaryStale checks if the binary on disk is newer than when this process started
func (s *MCPServer) IsBinaryStale() bool {
	if s.binaryPath == "" || s.binaryModTime.IsZero() {
		return false
	}

	info, err := os.Stat(s.binaryPath)
	if err != nil {
		return false
	}

	return info.ModTime().After(s.binaryModTime)
}

// GetBinaryStaleWarning returns a warning message if the binary is stale, or empty string if not
func (s *MCPServer) GetBinaryStaleWarning() string {
	if !s.IsBinaryStale() {
		return ""
	}
	return "CKB binary has been updated. Restart Claude Code or run /clear for changes to take effect."
}

// SendNotification sends a JSON-RPC notification to the client
func (s *MCPServer) SendNotification(method string, params interface{}) error {
	msg := &MCPMessage{
		Jsonrpc: "2.0",
		Method:  method,
		Params:  params,
	}
	return s.writeMessage(msg)
}
