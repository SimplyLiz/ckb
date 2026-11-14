package git

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/repostate"
)

const (
	// BackendID is the unique identifier for the Git backend
	BackendID = "git"

	// DefaultQueryTimeout is the default timeout for git operations (5000ms)
	DefaultQueryTimeout = 5000 * time.Millisecond
)

// GitAdapter implements the GitBackend interface
type GitAdapter struct {
	repoRoot     string
	queryTimeout time.Duration
	logger       *slog.Logger
	enabled      bool
}

// NewGitAdapter creates a new Git backend adapter
// Git backend is always enabled per design ("git is always")
func NewGitAdapter(cfg *config.Config, logger *slog.Logger) (*GitAdapter, error) {
	if logger == nil {
		return nil, errors.NewCkbError(
			errors.InternalError,
			"Logger is required for GitAdapter",
			nil,
			nil,
			nil,
		)
	}

	// Get query timeout from config, default to 5000ms
	timeout := DefaultQueryTimeout
	if timeoutMs, ok := cfg.QueryPolicy.TimeoutMs[BackendID]; ok && timeoutMs > 0 {
		timeout = time.Duration(timeoutMs) * time.Millisecond
	}

	// Git is always enabled per design
	enabled := true
	if !cfg.Backends.Git.Enabled {
		logger.Warn("Git backend is disabled in config, but Git is always available",
			"backend", BackendID)
	}

	adapter := &GitAdapter{
		repoRoot:     cfg.RepoRoot,
		queryTimeout: timeout,
		logger:       logger,
		enabled:      enabled,
	}

	// Verify git is available
	if !adapter.IsAvailable() {
		return nil, errors.NewCkbError(
			errors.BackendUnavailable,
			"Git is not available in this repository",
			nil,
			[]errors.FixAction{
				{
					Type:        errors.RunCommand,
					Command:     "git status",
					Safe:        true,
					Description: "Verify you're in a git repository",
				},
				{
					Type:        errors.RunCommand,
					Command:     "git init",
					Safe:        false,
					Description: "Initialize a git repository",
				},
			},
			nil,
		)
	}

	logger.Info("Git adapter initialized",
		"backend", BackendID,
		"repoRoot", cfg.RepoRoot,
		"timeout", timeout.String(),
		"enabled", enabled)

	return adapter, nil
}

// ID returns the backend identifier
func (g *GitAdapter) ID() string {
	return BackendID
}

// IsAvailable checks if git is available and this is a git repository
func (g *GitAdapter) IsAvailable() bool {
	if !g.enabled {
		return false
	}
	return repostate.IsGitRepository(g.repoRoot)
}

// Capabilities returns the list of capabilities this backend supports
func (g *GitAdapter) Capabilities() []string {
	return []string{
		"repo-state",
		"file-history",
		"churn-metrics",
		"blame-info",
		"diff-stats",
		"hotspots",
	}
}

// isAuthError returns true if the error looks like git failed to authenticate
// against the remote. Used to give the user an actionable message instead of
// a raw git stderr dump.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, marker := range []string{
		"authentication failed",
		"could not read username",
		"could not read password",
		"terminal prompts disabled",
		"403 forbidden",
		"401 unauthorized",
		"permission denied (publickey)",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	// "repository '...' not found" is typically an auth failure on private repos.
	if strings.Contains(s, "repository") && strings.Contains(s, "not found") {
		return true
	}
	return false
}

// VerifyRef returns nil iff ref resolves to a commit in the local repo.
// Unlike EnsureRef it never fetches. Use when auto-fetch is disabled.
func (g *GitAdapter) VerifyRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("empty ref")
	}
	if _, err := g.executeGitCommand("rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return fmt.Errorf("ref %q not present locally (auto-fetch disabled)", ref)
	}
	return nil
}

// EnsureRef returns a locally-resolvable form of the given ref, fetching
// from origin if needed. Handles shallow CI clones that only fetch the PR
// branch (Azure Pipelines, GitHub Actions defaults, GitLab default, etc.) —
// without this, `ckb review --base=main` fails with exit 128 because main
// isn't present locally.
//
// Returns the input ref if it already resolves; otherwise returns
// "origin/<branch>" after fetching. Never writes to local branches.
// MergeBase returns the commit a branch at head forked from base, the base a
// pull request is reviewed against. `git diff base head` compares the two tips
// instead, so whatever base gained since the fork shows up as the change,
// reversed.
func (g *GitAdapter) MergeBase(base, head string) (string, error) {
	out, err := g.executeGitCommand("merge-base", base, head)
	if err != nil {
		return "", err
	}
	mb := strings.TrimSpace(out)
	if mb == "" {
		return "", fmt.Errorf("no merge base between %s and %s", base, head)
	}
	return mb, nil
}

func (g *GitAdapter) EnsureRef(ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("empty ref")
	}
	if _, err := g.executeGitCommand("rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
		return ref, nil
	}

	branch := strings.TrimPrefix(ref, "refs/heads/")
	branch = strings.TrimPrefix(branch, "refs/remotes/origin/")
	branch = strings.TrimPrefix(branch, "origin/")
	if branch == "" {
		return "", fmt.Errorf("ref %q not found locally and could not derive branch name to fetch", ref)
	}

	g.logger.Info("Base ref not found locally; fetching from origin", "ref", ref, "branch", branch)
	if _, err := g.executeGitCommand("fetch", "--no-tags", "origin", branch); err != nil {
		if isAuthError(err) {
			return "", fmt.Errorf("ref %q not found locally and fetching from origin failed due to missing credentials. "+
				"Your CI checkout likely stripped the auth token — enable persistCredentials (Azure Pipelines, GitHub Actions) "+
				"or run `ckb review --no-auto-fetch` with a pre-fetched base ref. Underlying error: %w", ref, err)
		}
		return "", fmt.Errorf("ref %q not found locally and `git fetch origin %s` failed: %w", ref, branch, err)
	}

	if _, err := g.executeGitCommand("rev-parse", "--verify", "--quiet", "origin/"+branch+"^{commit}"); err == nil {
		return "origin/" + branch, nil
	}
	if _, err := g.executeGitCommand("rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
		return ref, nil
	}
	return "", fmt.Errorf("ref %q still not resolvable after fetch from origin", ref)
}

// GetHeadAuthorEmail returns the author email of the HEAD commit.
func (g *GitAdapter) GetHeadAuthorEmail() (string, error) {
	output, err := g.executeGitCommand("log", "-1", "--format=%ae", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// executeGitCommand runs a git command with timeout and returns the output
func (g *GitAdapter) executeGitCommand(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.queryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.repoRoot

	g.logger.Debug("Executing git command",
		"args", args,
		"timeout", g.queryTimeout.String(),
	)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", errors.NewCkbError(
				errors.Timeout,
				"Git command timed out",
				err,
				nil,
				nil,
			)
		}

		// Check if it's an exit error with stderr
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			msg := fmt.Sprintf("git %s: %s", strings.Join(args, " "), err)
			if stderr != "" {
				msg = fmt.Sprintf("git %s: %s: %s", strings.Join(args, " "), err, stderr)
			}
			return "", errors.NewCkbError(
				errors.InternalError,
				msg,
				err,
				nil,
				nil,
			).WithDetails(map[string]interface{}{
				"args":   args,
				"stderr": stderr,
			})
		}

		return "", errors.NewCkbError(
			errors.InternalError,
			"Failed to execute git command",
			err,
			nil,
			nil,
		)
	}

	return strings.TrimSpace(string(output)), nil
}

// executeGitCommandLines runs a git command and returns output as lines
func (g *GitAdapter) executeGitCommandLines(args ...string) ([]string, error) {
	output, err := g.executeGitCommand(args...)
	if err != nil {
		return nil, err
	}

	if output == "" {
		return []string{}, nil
	}

	lines := strings.Split(output, "\n")
	// Filter out empty lines
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result, nil
}
