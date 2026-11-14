package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/auth"
	"github.com/SimplyLiz/CodeMCP/internal/query"

	"github.com/google/uuid"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const (
	requestIDKey  contextKey = "requestID"
	authResultKey contextKey = "authResult"
)

// LoggingMiddleware logs HTTP requests and responses
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Get request ID from context
			reqID := GetRequestID(r.Context())

			// Log request
			logger.Info("HTTP request",
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"remoteAddr", r.RemoteAddr,
				"requestID", reqID,
			)

			// Call next handler
			next.ServeHTTP(wrapped, r)

			// Log response
			duration := time.Since(start)
			logger.Info("HTTP response",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.statusCode,
				"duration", duration.String(),
				"durationMs", duration.Milliseconds(),
				"requestID", reqID,
			)
		})
	}
}

// IndexRefreshMiddleware picks up a SCIP index that 'ckb index' rewrote while
// this server was running (#134): one os.Stat per request when nothing
// changed, a single shared reload when it did.
func IndexRefreshMiddleware(engine *query.Engine, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if engine != nil {
				if _, err := engine.EnsureSCIPLoaded(r.Context()); err != nil {
					logger.Warn("SCIP index changed on disk but reloading it failed", "error", err.Error())
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RecoveryMiddleware recovers from panics and logs them
func RecoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					reqID := GetRequestID(r.Context())
					logger.Error("Panic recovered",
						"error", fmt.Sprintf("%v", err),
						"stack", string(debug.Stack()),
						"requestID", reqID,
					)

					// Return 500 error
					InternalError(w, "Internal server error", fmt.Errorf("%v", err))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// CORSConfig contains CORS configuration
type CORSConfig struct {
	AllowedOrigins []string // Empty or ["*"] means allow all
	AllowedMethods []string
	AllowedHeaders []string
}

// DefaultCORSConfig returns a restrictive default CORS configuration
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowedOrigins: []string{}, // Empty = no CORS (same-origin only)
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization", "X-Request-ID"},
	}
}

// CORSMiddleware adds CORS headers based on configuration
func CORSMiddleware(config CORSConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Determine allowed origin
			allowedOrigin := ""
			if len(config.AllowedOrigins) == 0 {
				// No CORS configured - don't set any CORS headers (same-origin only)
			} else if len(config.AllowedOrigins) == 1 && config.AllowedOrigins[0] == "*" {
				// Allow all origins
				allowedOrigin = "*"
			} else {
				// Check if origin is in allowed list
				for _, allowed := range config.AllowedOrigins {
					if allowed == origin {
						allowedOrigin = origin
						break
					}
				}
			}

			// Set CORS headers only if origin is allowed
			if allowedOrigin != "" {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
				if allowedOrigin != "*" {
					w.Header().Set("Vary", "Origin")
				}

				methods := "GET, POST, PUT, DELETE, OPTIONS"
				if len(config.AllowedMethods) > 0 {
					methods = ""
					for i, m := range config.AllowedMethods {
						if i > 0 {
							methods += ", "
						}
						methods += m
					}
				}
				w.Header().Set("Access-Control-Allow-Methods", methods)

				headers := "Content-Type, Authorization, X-Request-ID"
				if len(config.AllowedHeaders) > 0 {
					headers = ""
					for i, h := range config.AllowedHeaders {
						if i > 0 {
							headers += ", "
						}
						headers += h
					}
				}
				w.Header().Set("Access-Control-Allow-Headers", headers)
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			// Handle preflight requests
			if r.Method == "OPTIONS" {
				if allowedOrigin != "" {
					w.WriteHeader(http.StatusOK)
				} else {
					w.WriteHeader(http.StatusForbidden)
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// AuthConfig contains authentication configuration
type AuthConfig struct {
	Enabled bool
	Token   string
}

// AuthMiddleware enforces token-based authentication for mutating requests
func AuthMiddleware(config AuthConfig, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth if disabled
			if !config.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Allow read-only methods without auth
			if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
				next.ServeHTTP(w, r)
				return
			}

			// Check Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				logger.Warn("Missing authorization header",
					"path", r.URL.Path,
					"method", r.Method,
					"requestID", GetRequestID(r.Context()),
				)
				http.Error(w, `{"error":"unauthorized","message":"missing Authorization header"}`, http.StatusUnauthorized)
				return
			}

			// Expect "Bearer <token>" format
			const bearerPrefix = "Bearer "
			if len(authHeader) < len(bearerPrefix) || authHeader[:len(bearerPrefix)] != bearerPrefix {
				logger.Warn("Invalid authorization format",
					"path", r.URL.Path,
					"method", r.Method,
					"requestID", GetRequestID(r.Context()),
				)
				http.Error(w, `{"error":"unauthorized","message":"invalid Authorization format, expected 'Bearer <token>'"}`, http.StatusUnauthorized)
				return
			}

			token := authHeader[len(bearerPrefix):]
			if token != config.Token {
				logger.Warn("Invalid auth token",
					"path", r.URL.Path,
					"method", r.Method,
					"requestID", GetRequestID(r.Context()),
				)
				http.Error(w, `{"error":"forbidden","message":"invalid token"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ScopedAuthMiddleware enforces scoped token-based authentication
// This is the Phase 4 middleware that supports API keys with scopes and rate limiting
func ScopedAuthMiddleware(authManager *auth.Manager, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exempt health check endpoints from authentication
			// These must remain accessible for Kubernetes probes and load balancer health checks
			path := r.URL.Path
			if path == "/health" || path == "/ready" || path == "/health/detailed" {
				next.ServeHTTP(w, r)
				return
			}

			// Determine required scope from method and path
			scope := determineRequiredScope(r)
			repoID := extractRepoIDFromRequest(r)

			// Get token from Authorization header
			token := extractBearerToken(r)

			// Authenticate
			result := authManager.Authenticate(token, scope, repoID)

			// Add auth info to context
			ctx := context.WithValue(r.Context(), authResultKey, result)
			r = r.WithContext(ctx)

			// Check rate limiting first (rate limited is a valid auth but over limit)
			if result.RateLimited {
				logger.Info("Rate limited",
					"path", r.URL.Path,
					"key_id", result.KeyID,
					"retry_after", result.RetryAfter,
					"requestID", GetRequestID(r.Context()),
				)

				w.Header().Set("Retry-After", strconv.Itoa(result.RetryAfter))
				w.Header().Set("X-RateLimit-Remaining", "0")
				writeAuthError(w, result)
				return
			}

			if !result.Authenticated {
				logger.Warn("Authentication failed",
					"path", r.URL.Path,
					"method", r.Method,
					"error_code", result.ErrorCode,
					"requestID", GetRequestID(r.Context()),
				)

				writeAuthError(w, result)
				return
			}

			// Add rate limit headers for successful requests
			if result.KeyID != "" {
				w.Header().Set("X-RateLimit-Key", result.KeyID)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// determineRequiredScope maps HTTP method/path to required scope
func determineRequiredScope(r *http.Request) auth.Scope {
	path := r.URL.Path

	// DELETE requests require admin scope
	if r.Method == "DELETE" {
		return auth.ScopeAdmin
	}

	// Token management requires admin scope
	if strings.HasPrefix(path, "/tokens") {
		if r.Method == "POST" || r.Method == "DELETE" {
			return auth.ScopeAdmin
		}
	}

	// POST and PUT requests require write scope
	if r.Method == "POST" || r.Method == "PUT" {
		return auth.ScopeWrite
	}

	// Everything else (GET, HEAD, OPTIONS) requires read scope
	return auth.ScopeRead
}

// extractRepoIDFromRequest extracts repo ID from URL path
// Handles paths like /index/repos/{org}/{repo}/... or /index/repos/{repo}/...
func extractRepoIDFromRequest(r *http.Request) string {
	path := r.URL.Path

	// Look for /index/repos/{repo}/ pattern
	if !strings.HasPrefix(path, "/index/repos/") {
		return ""
	}

	// Remove prefix
	remaining := strings.TrimPrefix(path, "/index/repos/")
	if remaining == "" {
		return ""
	}

	// Find the repo ID (everything before the next action segment)
	// Possible patterns:
	// - org/repo/symbols -> "org/repo"
	// - org/repo/upload -> "org/repo"
	// - simple-repo/files -> "simple-repo"

	// Known action segments that mark the end of repo ID
	actions := []string{"/symbols", "/files", "/refs", "/callgraph", "/meta", "/upload", "/search"}

	repoID := remaining
	for _, action := range actions {
		if idx := strings.Index(remaining, action); idx > 0 {
			repoID = remaining[:idx]
			break
		}
	}

	// Clean up trailing slash
	repoID = strings.TrimSuffix(repoID, "/")

	return repoID
}

// extractBearerToken extracts the bearer token from Authorization header
func extractBearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}

	const bearerPrefix = "Bearer "
	if len(authHeader) < len(bearerPrefix) || !strings.HasPrefix(authHeader, bearerPrefix) {
		return ""
	}

	return authHeader[len(bearerPrefix):]
}

// writeAuthError writes an authentication error response
func writeAuthError(w http.ResponseWriter, result *auth.AuthResult) {
	var status int
	switch result.ErrorCode {
	case auth.ErrCodeMissingToken, auth.ErrCodeInvalidToken, auth.ErrCodeExpiredToken, auth.ErrCodeRevokedToken:
		status = http.StatusUnauthorized
	case auth.ErrCodeInsufficientScope, auth.ErrCodeRepoNotAllowed:
		status = http.StatusForbidden
	case auth.ErrCodeRateLimited:
		status = http.StatusTooManyRequests
	default:
		status = http.StatusUnauthorized
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	response := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    result.ErrorCode,
			"message": result.ErrorMessage,
		},
	}

	if result.RetryAfter > 0 {
		if errMap, ok := response["error"].(map[string]interface{}); ok {
			errMap["retry_after"] = result.RetryAfter
		}
	}

	_ = json.NewEncoder(w).Encode(response)
}

// GetAuthResult retrieves the auth result from context
func GetAuthResult(ctx context.Context) *auth.AuthResult {
	if result, ok := ctx.Value(authResultKey).(*auth.AuthResult); ok {
		return result
	}
	return nil
}

// RequestIDMiddleware adds a unique request ID to each request
func RequestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if request ID already exists in header
			reqID := r.Header.Get("X-Request-ID")
			if reqID == "" {
				// Generate new request ID
				reqID = uuid.New().String()
			}

			// Add request ID to context
			ctx := context.WithValue(r.Context(), requestIDKey, reqID)
			r = r.WithContext(ctx)

			// Add request ID to response header
			w.Header().Set("X-Request-ID", reqID)

			next.ServeHTTP(w, r)
		})
	}
}

// GetRequestID retrieves the request ID from context
func GetRequestID(ctx context.Context) string {
	if reqID, ok := ctx.Value(requestIDKey).(string); ok {
		return reqID
	}
	return ""
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before writing it
func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

// Write ensures status code is set if WriteHeader wasn't called
func (rw *responseWriter) Write(data []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	return rw.ResponseWriter.Write(data)
}
