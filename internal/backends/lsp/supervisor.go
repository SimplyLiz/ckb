package lsp

import (
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
)

// Constants for supervisor behavior
const (
	// MaxTotalProcesses is the default max number of concurrent LSP processes
	MaxTotalProcesses = 4

	// QueueSizePerLanguage is the default queue size per language
	QueueSizePerLanguage = 10

	// MaxQueueWaitMs is the default max time to wait for a queue slot
	MaxQueueWaitMs = 200

	// MaxConsecutiveFailures before marking a process as unhealthy
	MaxConsecutiveFailures = 3

	// BaseBackoffMs is the base backoff duration in milliseconds
	BaseBackoffMs = 1000

	// MaxBackoffMs is the maximum backoff duration in milliseconds
	MaxBackoffMs = 30000

	// HealthCheckInterval is how often to check process health
	HealthCheckInterval = 30 * time.Second

	// ResponseTimeout is how long to wait before considering a process unhealthy
	ResponseTimeout = 60 * time.Second
)

// LspSupervisor manages multiple LSP server processes
type LspSupervisor struct {
	// processes maps language ID to LSP process
	processes map[string]*LspProcess

	// config contains the configuration
	config *config.Config

	// logger for logging
	logger *slog.Logger

	// mu protects processes map
	mu sync.RWMutex

	// queues maps language ID to request queue
	queues map[string]chan *LspRequest

	// queuesMu protects queues map
	queuesMu sync.RWMutex

	// done signals shutdown
	done chan struct{}

	// wg tracks background goroutines
	wg sync.WaitGroup

	// maxProcesses limits concurrent processes
	maxProcesses int

	// queueSize is the queue size per language
	queueSize int

	// maxQueueWaitMs is the max wait time for a queue slot
	maxQueueWaitMs int
}

// NewLspSupervisor creates a new LSP supervisor
func NewLspSupervisor(cfg *config.Config, logger *slog.Logger) *LspSupervisor {
	maxProcesses := cfg.LspSupervisor.MaxTotalProcesses
	if maxProcesses == 0 {
		maxProcesses = MaxTotalProcesses
	}

	queueSize := cfg.LspSupervisor.QueueSizePerLanguage
	if queueSize == 0 {
		queueSize = QueueSizePerLanguage
	}

	maxQueueWaitMs := cfg.LspSupervisor.MaxQueueWaitMs
	if maxQueueWaitMs == 0 {
		maxQueueWaitMs = MaxQueueWaitMs
	}

	s := &LspSupervisor{
		processes:      make(map[string]*LspProcess),
		config:         cfg,
		logger:         logger,
		queues:         make(map[string]chan *LspRequest),
		done:           make(chan struct{}),
		maxProcesses:   maxProcesses,
		queueSize:      queueSize,
		maxQueueWaitMs: maxQueueWaitMs,
	}

	// Start health check loop
	s.wg.Add(1)
	go s.healthCheckLoop()

	return s
}

// StartServer starts an LSP server for the given language
func (s *LspSupervisor) StartServer(languageId string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already running
	if proc, exists := s.processes[languageId]; exists {
		if proc.IsHealthy() {
			return nil // Already running and healthy
		}
		// If unhealthy, shut it down first
		_ = proc.Shutdown()
		delete(s.processes, languageId)
	}

	// Check capacity
	if len(s.processes) >= s.maxProcesses {
		if err := s.ensureCapacity(); err != nil {
			return fmt.Errorf("cannot start server, at capacity: %w", err)
		}
	}

	// Get server config
	serverCfg, ok := s.config.Backends.Lsp.Servers[languageId]
	if !ok {
		return errors.NewCkbError(
			errors.BackendUnavailable,
			fmt.Sprintf("no LSP server configured for language: %s", languageId),
			nil,
			nil,
			nil,
		)
	}

	// Create process
	proc := NewLspProcess(languageId, s.config.RepoRoot)

	// Build command
	cmd := exec.Command(serverCfg.Command, serverCfg.Args...) // #nosec G204 //nolint:gosec // command from trusted LSP config
	cmd.Dir = s.config.RepoRoot

	// Set up pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	proc.stdin = stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	proc.stdout = stdout

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	proc.stderr = stderr

	// Start process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start LSP server: %w", err)
	}

	proc.cmd = cmd

	// Start read loops
	go proc.readLoop()
	go proc.stderrLoop()

	// Send initialize request
	if err := s.initializeServer(proc); err != nil {
		_ = proc.Shutdown()
		return fmt.Errorf("failed to initialize LSP server: %w", err)
	}

	// Store process
	s.processes[languageId] = proc

	// Create queue if it doesn't exist
	s.queuesMu.Lock()
	if _, exists := s.queues[languageId]; !exists {
		s.queues[languageId] = make(chan *LspRequest, s.queueSize)
		// Start queue processor
		s.wg.Add(1)
		go s.processQueue(languageId)
	}
	s.queuesMu.Unlock()

	s.logger.Info("Started LSP server",
		"languageId", languageId,
		"command", serverCfg.Command)

	return nil
}

// initializeServer sends the initialize request to the LSP server
func (s *LspSupervisor) initializeServer(proc *LspProcess) error {
	proc.SetState(StateInitializing)

	// Build initialize params
	params := map[string]interface{}{
		"processId": nil,
		"rootUri":   fmt.Sprintf("file://%s", proc.WorkspaceRoot),
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"definition": map[string]interface{}{
					"linkSupport": true,
				},
				"references": map[string]interface{}{},
				"documentSymbol": map[string]interface{}{
					"hierarchicalDocumentSymbolSupport": true,
				},
			},
			"workspace": map[string]interface{}{
				"symbol": map[string]interface{}{},
			},
		},
	}

	// Send initialize request
	result, err := proc.sendRequest("initialize", params)
	if err != nil {
		return fmt.Errorf("initialize request failed: %w", err)
	}

	// Extract capabilities
	if resultMap, ok := result.(map[string]interface{}); ok {
		if caps, ok := resultMap["capabilities"].(map[string]interface{}); ok {
			proc.SetCapabilities(caps)
		}
	}

	// Send initialized notification
	if err := proc.sendNotification("initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("initialized notification failed: %w", err)
	}

	proc.SetState(StateReady)
	proc.RecordSuccess()

	return nil
}

// StopServer stops an LSP server
func (s *LspSupervisor) StopServer(languageId string) error {
	s.mu.Lock()
	proc, exists := s.processes[languageId]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("no server running for language: %s", languageId)
	}
	delete(s.processes, languageId)
	s.mu.Unlock()

	return proc.Shutdown()
}

// GetProcess returns the process for a language (or nil if not running)
func (s *LspSupervisor) GetProcess(languageId string) *LspProcess {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.processes[languageId]
}

// IsReady returns true if the server is ready to handle requests
func (s *LspSupervisor) IsReady(languageId string) bool {
	proc := s.GetProcess(languageId)
	if proc == nil {
		return false
	}
	return proc.IsHealthy()
}

// healthCheckLoop periodically checks process health
func (s *LspSupervisor) healthCheckLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.checkAllProcesses()
		case <-s.done:
			return
		}
	}
}

// checkAllProcesses checks health of all running processes
func (s *LspSupervisor) checkAllProcesses() {
	s.mu.RLock()
	languageIds := make([]string, 0, len(s.processes))
	for langId := range s.processes {
		languageIds = append(languageIds, langId)
	}
	s.mu.RUnlock()

	for _, langId := range languageIds {
		if !s.checkHealth(langId) {
			s.handleCrash(langId)
		}
	}
}

// Shutdown stops all LSP servers and cleans up
func (s *LspSupervisor) Shutdown() error {
	close(s.done)

	// Stop all processes
	s.mu.Lock()
	for langId, proc := range s.processes {
		s.logger.Info("Shutting down LSP server", "languageId", langId)
		_ = proc.Shutdown()
	}
	s.processes = make(map[string]*LspProcess)
	s.mu.Unlock()

	// Close all queues
	s.queuesMu.Lock()
	for _, queue := range s.queues {
		close(queue)
	}
	s.queues = make(map[string]chan *LspRequest)
	s.queuesMu.Unlock()

	// Wait for goroutines
	s.wg.Wait()

	return nil
}

// ServerAvailability describes one configured LSP server.
// Running reports whether a process is currently held for the language.
// OnPath reports whether the server command resolves via exec.LookPath.
type ServerAvailability struct {
	Language string
	Command  string
	OnPath   bool
	Running  bool
}

// ConfiguredServers lists every server in config.Backends.Lsp.Servers, sorted by
// language. Servers start lazily on the first query, so Running is false until then;
// OnPath tells callers whether the server can be started at all.
func (s *LspSupervisor) ConfiguredServers() []ServerAvailability {
	s.mu.RLock()
	defer s.mu.RUnlock()

	langs := make([]string, 0, len(s.config.Backends.Lsp.Servers))
	for lang := range s.config.Backends.Lsp.Servers {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	out := make([]ServerAvailability, 0, len(langs))
	for _, lang := range langs {
		cmd := s.config.Backends.Lsp.Servers[lang].Command
		_, running := s.processes[lang]
		_, err := exec.LookPath(cmd)
		out = append(out, ServerAvailability{
			Language: lang,
			Command:  cmd,
			OnPath:   err == nil,
			Running:  running,
		})
	}
	return out
}

// GetStats returns statistics about running processes
func (s *LspSupervisor) GetStats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]interface{}{
		"totalProcesses": len(s.processes),
		"maxProcesses":   s.maxProcesses,
		"processes":      make(map[string]interface{}),
	}

	for langId, proc := range s.processes {
		stats["processes"].(map[string]interface{})[langId] = map[string]interface{}{ //nolint:errcheck // type assertion, not function call
			"state":               proc.GetState(),
			"restartCount":        proc.GetRestartCount(),
			"consecutiveFailures": proc.GetConsecutiveFailures(),
			"lastResponse":        proc.GetLastResponseTime(),
		}
	}

	return stats
}
