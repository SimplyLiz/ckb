// Package repos provides multi-repository management with a global registry.
package repos

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/paths"
)

// RepoState represents the current state of a registered repository.
type RepoState string

const (
	RepoStateValid         RepoState = "valid"         // Path exists, .ckb/ initialized
	RepoStateUninitialized RepoState = "uninitialized" // Path exists, no .ckb/
	RepoStateMissing       RepoState = "missing"       // Path doesn't exist
)

// RepoEntry represents a registered repository.
type RepoEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"` // Always absolute, cleaned
	AddedAt    time.Time `json:"added_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// Registry stores the global repository registry.
type Registry struct {
	Repos   map[string]RepoEntry `json:"repos"`
	Default string               `json:"default,omitempty"`
	Version int                  `json:"version"`
}

const currentRegistryVersion = 1

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// GetRegistryPath returns the path to the global registry file, under
// $CKB_HOME like the rest of CKB's global state (default ~/.ckb). It used to
// ignore CKB_HOME, so a test or a sandboxed run registered its temp repos in
// the user's real registry and could make one the default.
func GetRegistryPath() (string, error) {
	home, err := paths.GetCKBHome()
	if err != nil {
		return "", fmt.Errorf("failed to get CKB home directory: %w", err)
	}
	return filepath.Join(home, "repos.json"), nil
}

// GetLockPath returns the path to the registry lock file.
func GetLockPath() (string, error) {
	home, err := paths.GetCKBHome()
	if err != nil {
		return "", fmt.Errorf("failed to get CKB home directory: %w", err)
	}
	return filepath.Join(home, "repos.lock"), nil
}

// LoadRegistry loads the registry from disk.
func LoadRegistry() (*Registry, error) {
	path, err := GetRegistryPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) // #nosec G703 -- path is internally constructed
	if os.IsNotExist(err) {
		// Return empty registry
		return &Registry{
			Repos:   make(map[string]RepoEntry),
			Version: currentRegistryVersion,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read registry: %w", err)
	}

	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("failed to parse registry: %w", err)
	}

	// Version check
	if reg.Version > currentRegistryVersion {
		return nil, fmt.Errorf("registry version %d not supported (max: %d)", reg.Version, currentRegistryVersion)
	}

	// Initialize map if nil
	if reg.Repos == nil {
		reg.Repos = make(map[string]RepoEntry)
	}

	return &reg, nil
}

// Save persists the registry to disk with file locking.
func (r *Registry) Save() error {
	path, err := GetRegistryPath()
	if err != nil {
		return err
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if mkdirErr := os.MkdirAll(dir, 0755); mkdirErr != nil {
		return fmt.Errorf("failed to create registry directory: %w", mkdirErr)
	}

	// Acquire lock
	lockPath, err := GetLockPath()
	if err != nil {
		return err
	}
	lock, err := acquireLock(lockPath)
	if err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	// Ensure version is set
	r.Version = currentRegistryVersion

	// Marshal with indentation for human readability
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal registry: %w", err)
	}

	// Write atomically
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil { // #nosec G703 -- non-sensitive registry file
		return fmt.Errorf("failed to write registry: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath) // #nosec G703 -- path is internally constructed
		return fmt.Errorf("failed to rename registry: %w", err)
	}

	return nil
}

// ValidateName checks if a repo name is valid.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("repo name cannot be empty")
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("repo name must contain only letters, numbers, underscores, and hyphens")
	}
	return nil
}

// Add registers a new repository.
func (r *Registry) Add(name, path string) error {
	if err := ValidateName(name); err != nil {
		return err
	}

	if _, exists := r.Repos[name]; exists {
		return fmt.Errorf("repo '%s' already exists", name)
	}

	// Normalize path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}
	absPath = filepath.Clean(absPath)

	// Check path exists
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("path does not exist: %s", absPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", absPath)
	}

	r.Repos[name] = RepoEntry{
		Name:    name,
		Path:    absPath,
		AddedAt: time.Now(),
	}

	return r.Save()
}

// Remove unregisters a repository.
func (r *Registry) Remove(name string) error {
	if _, exists := r.Repos[name]; !exists {
		return fmt.Errorf("repo '%s' not found", name)
	}

	delete(r.Repos, name)

	// Clear default if it was this repo
	if r.Default == name {
		r.Default = ""
	}

	return r.Save()
}

// Rename changes a repo's name.
func (r *Registry) Rename(oldName, newName string) error {
	if err := ValidateName(newName); err != nil {
		return err
	}

	entry, exists := r.Repos[oldName]
	if !exists {
		return fmt.Errorf("repo '%s' not found", oldName)
	}

	if _, exists := r.Repos[newName]; exists {
		return fmt.Errorf("repo '%s' already exists", newName)
	}

	entry.Name = newName
	r.Repos[newName] = entry
	delete(r.Repos, oldName)

	// Update default if needed
	if r.Default == oldName {
		r.Default = newName
	}

	return r.Save()
}

// Get returns a repo entry and its current state.
func (r *Registry) Get(name string) (*RepoEntry, RepoState, error) {
	entry, exists := r.Repos[name]
	if !exists {
		return nil, "", fmt.Errorf("repo '%s' not found", name)
	}

	state := r.ValidateState(name)
	return &entry, state, nil
}

// GetByPath finds a repo by its path.
func (r *Registry) GetByPath(path string) (*RepoEntry, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absPath = filepath.Clean(absPath)

	for _, entry := range r.Repos {
		if entry.Path == absPath {
			return &entry, nil
		}
	}
	return nil, fmt.Errorf("no repo registered for path: %s", absPath)
}

// List returns all registered repos.
func (r *Registry) List() []RepoEntry {
	entries := make([]RepoEntry, 0, len(r.Repos))
	for _, entry := range r.Repos {
		entries = append(entries, entry)
	}
	return entries
}

// SortByLastUsed sorts repo entries by last used time (most recent first),
// falling back to alphabetical by name for entries without timestamps.
func SortByLastUsed(entries []RepoEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastUsedAt.IsZero() && entries[j].LastUsedAt.IsZero() {
			return entries[i].Name < entries[j].Name
		}
		if entries[i].LastUsedAt.IsZero() {
			return false
		}
		if entries[j].LastUsedAt.IsZero() {
			return true
		}
		return entries[i].LastUsedAt.After(entries[j].LastUsedAt)
	})
}

// SetDefault sets the default repo.
func (r *Registry) SetDefault(name string) error {
	if name != "" {
		if _, exists := r.Repos[name]; !exists {
			return fmt.Errorf("repo '%s' not found", name)
		}
	}
	r.Default = name
	return r.Save()
}

// GetDefault returns the default repo name.
func (r *Registry) GetDefault() string {
	return r.Default
}

// TouchLastUsed updates the last used timestamp.
func (r *Registry) TouchLastUsed(name string) error {
	entry, exists := r.Repos[name]
	if !exists {
		return fmt.Errorf("repo '%s' not found", name)
	}
	entry.LastUsedAt = time.Now()
	r.Repos[name] = entry
	return r.Save()
}

// ValidateState checks the current state of a repo.
func (r *Registry) ValidateState(name string) RepoState {
	entry, exists := r.Repos[name]
	if !exists {
		return RepoStateMissing
	}

	// Check if path exists
	info, err := os.Stat(entry.Path)
	if os.IsNotExist(err) || err != nil {
		return RepoStateMissing
	}
	if !info.IsDir() {
		return RepoStateMissing
	}

	// Check if .ckb exists
	ckbPath := filepath.Join(entry.Path, ".ckb")
	if _, err := os.Stat(ckbPath); os.IsNotExist(err) {
		return RepoStateUninitialized
	}

	return RepoStateValid
}

// FileLock represents a file-based lock.
type FileLock struct {
	file *os.File
}

// Release releases the file lock.
func (l *FileLock) Release() error {
	if l.file != nil {
		_ = unlockFile(l.file)
		_ = l.file.Close()
		l.file = nil
	}
	return nil
}

func acquireLock(path string) (*FileLock, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644) // #nosec G703 -- path is internally constructed
	if err != nil {
		return nil, err
	}

	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}

	return &FileLock{file: f}, nil
}
