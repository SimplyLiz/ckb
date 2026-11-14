package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/auth"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// ServerConfig contains configuration for the HTTP server
type ServerConfig struct {
	Auth        AuthConfig
	CORS        CORSConfig
	Metrics     MetricsConfig
	IndexServer *IndexServerConfig // nil if index-server mode disabled
}

// DefaultServerConfig returns default server configuration
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Auth: AuthConfig{
			Enabled: true,
			Token:   "", // Must be set explicitly
		},
		CORS:    DefaultCORSConfig(),
		Metrics: DefaultMetricsConfig(),
	}
}

// Server represents the HTTP API server
type Server struct {
	router       *http.ServeMux
	server       *http.Server
	addr         string
	logger       *slog.Logger
	engine       *query.Engine
	config       ServerConfig
	metrics      *MetricsCollector
	indexManager *IndexRepoManager // nil if index-server mode disabled
	authManager  *auth.Manager     // nil if scoped auth disabled
}

// NewServer creates a new HTTP server instance
func NewServer(addr string, engine *query.Engine, logger *slog.Logger, config ServerConfig) (*Server, error) {
	s := &Server{
		addr:   addr,
		logger: logger,
		engine: engine,
		router: http.NewServeMux(),
		config: config,
	}

	// Initialize metrics collector if enabled
	if config.Metrics.Enabled {
		s.metrics = NewMetricsCollector()
	}

	// Initialize index server if enabled
	if config.IndexServer != nil && config.IndexServer.Enabled {
		mgr, err := NewIndexRepoManager(config.IndexServer, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize index server: %w", err)
		}
		s.indexManager = mgr
		logger.Info("Index server enabled", "repo_count", len(config.IndexServer.Repos))

		// Initialize auth manager if enabled
		if config.IndexServer.Auth.Enabled {
			// Note: We pass nil for db since static keys don't need database
			// For dynamic key management, the database would be passed here
			authMgr, err := auth.NewManager(config.IndexServer.Auth, nil, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize auth manager: %w", err)
			}
			s.authManager = authMgr
			logger.Info("Scoped auth enabled",
				"static_keys", len(config.IndexServer.Auth.StaticKeys),
				"has_legacy", config.IndexServer.Auth.LegacyToken != "",
				"rate_limiting", config.IndexServer.Auth.RateLimiting.Enabled,
			)
		}
	}

	// Register routes
	s.registerRoutes()

	// Create HTTP server with configured router and middleware
	handler := s.applyMiddleware(s.router)
	s.server = &http.Server{
		Addr:        addr,
		Handler:     handler,
		ReadTimeout: 15 * time.Second,
		// WriteTimeout must accommodate long-running endpoints like /review/pr
		// and /architecture/refresh which can take several minutes on large repos.
		// Individual handlers enforce their own context deadlines.
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	return s, nil
}

// Start starts the HTTP server
func (s *Server) Start() error {
	s.logger.Info("Starting HTTP server", "addr", s.addr)

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down HTTP server")

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	// Close index manager if enabled
	if s.indexManager != nil {
		if err := s.indexManager.Close(); err != nil {
			s.logger.Warn("Failed to close index manager", "error", err.Error())
		}
	}

	s.logger.Info("Server shut down successfully")
	return nil
}

// ServeHTTP implements http.Handler for testing
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.server.Handler.ServeHTTP(w, r)
}

// applyMiddleware wraps the handler with middleware in the correct order
func (s *Server) applyMiddleware(handler http.Handler) http.Handler {
	// Apply middleware in reverse order (last one wraps first)
	// Order: CORS -> RequestID -> Auth -> Logging -> Recovery
	// (Recovery is outermost, CORS is innermost before handler)
	handler = IndexRefreshMiddleware(s.engine, s.logger)(handler)
	handler = RecoveryMiddleware(s.logger)(handler)
	handler = LoggingMiddleware(s.logger)(handler)

	// Use ScopedAuthMiddleware when auth manager is available (Phase 4),
	// otherwise fall back to legacy AuthMiddleware
	if s.authManager != nil {
		handler = ScopedAuthMiddleware(s.authManager, s.logger)(handler)
	} else {
		handler = AuthMiddleware(s.config.Auth, s.logger)(handler)
	}

	handler = RequestIDMiddleware()(handler)
	handler = CORSMiddleware(s.config.CORS)(handler)
	return handler
}
