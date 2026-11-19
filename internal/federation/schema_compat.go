package federation

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// MinSchemaVersion is the minimum schema version required for federation
const MinSchemaVersion = 6

// CompatibilityStatus represents the compatibility status of a repository
type CompatibilityStatus string

const (
	// CompatibilityOK means the repo is compatible
	CompatibilityOK CompatibilityStatus = "ok"

	// CompatibilityNeedsRefresh means the repo needs a refresh to migrate
	CompatibilityNeedsRefresh CompatibilityStatus = "needs_refresh"

	// CompatibilityNeedsMigration means the repo needs a schema migration
	CompatibilityNeedsMigration CompatibilityStatus = "needs_migration"

	// CompatibilityIncompatible means the repo is incompatible
	CompatibilityIncompatible CompatibilityStatus = "incompatible"

	// CompatibilityNotInitialized means the repo doesn't have CKB initialized
	CompatibilityNotInitialized CompatibilityStatus = "not_initialized"
)

// CompatibilityCheck contains the result of a compatibility check
type CompatibilityCheck struct {
	// RepoID is the repository identifier
	RepoID string `json:"repoId"`

	// Path is the repository path
	Path string `json:"path"`

	// SchemaVersion is the current schema version (0 if not initialized)
	SchemaVersion int `json:"schemaVersion"`

	// RequiredVersion is the minimum required version
	RequiredVersion int `json:"requiredVersion"`

	// Status is the compatibility status
	Status CompatibilityStatus `json:"status"`

	// Message is a human-readable message
	Message string `json:"message"`

	// Action is the recommended action
	Action string `json:"action,omitempty"`
}

// CheckSchemaCompatibility checks if a repository is compatible with federation
func CheckSchemaCompatibility(repoID, repoPath string) (*CompatibilityCheck, error) {
	check := &CompatibilityCheck{
		RepoID:          repoID,
		Path:            repoPath,
		RequiredVersion: MinSchemaVersion,
	}

	// Check if the .ckb directory exists
	ckbDir := filepath.Join(repoPath, ".ckb")
	if _, err := os.Stat(ckbDir); os.IsNotExist(err) {
		check.Status = CompatibilityNotInitialized
		check.Message = "Repository has not been initialized with CKB"
		check.Action = fmt.Sprintf("Run: cd %s && ckb init", repoPath)
		return check, nil
	}

	// Check if the database exists
	dbPath := filepath.Join(ckbDir, "ckb.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		check.Status = CompatibilityNotInitialized
		check.Message = "Repository database not found"
		check.Action = fmt.Sprintf("Run: cd %s && ckb index", repoPath)
		return check, nil
	}

	// Open the database and check schema version
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Try to get schema version from various possible tables
	version := getSchemaVersion(db)
	check.SchemaVersion = version

	if version == 0 {
		check.Status = CompatibilityNeedsRefresh
		check.Message = "Cannot determine schema version"
		check.Action = fmt.Sprintf("Run: cd %s && ckb refresh", repoPath)
		return check, nil
	}

	if version < MinSchemaVersion {
		check.Status = CompatibilityNeedsMigration
		check.Message = fmt.Sprintf("Schema version %d is below minimum %d", version, MinSchemaVersion)
		check.Action = fmt.Sprintf("Run: cd %s && ckb refresh --migrate", repoPath)
		return check, nil
	}

	check.Status = CompatibilityOK
	check.Message = "Repository is compatible"
	return check, nil
}

// getSchemaVersion attempts to read the schema version from the database
func getSchemaVersion(db *sql.DB) int {
	// Try schema_versions table first
	var version int
	err := db.QueryRow("SELECT MAX(version) FROM schema_versions").Scan(&version)
	if err == nil && version > 0 {
		return version
	}

	// Try schema_version table (singular)
	err = db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err == nil && version > 0 {
		return version
	}

	// Try to infer from table structure
	// If modules table has 'source' column, it's at least v6
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('modules')
		WHERE name = 'source'
	`).Scan(&count)
	if err == nil && count > 0 {
		return 6
	}

	// If symbol_mappings exists, it's at least v1
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='symbol_mappings'").Scan(&count)
	if err == nil && count > 0 {
		return 1
	}

	return 0
}

// CheckAllReposCompatibility checks compatibility for all repos in a federation
func CheckAllReposCompatibility(fed *Federation) ([]*CompatibilityCheck, error) {
	var results []*CompatibilityCheck

	for _, repo := range fed.ListRepos() {
		check, err := CheckSchemaCompatibility(repo.RepoID, repo.Path)
		if err != nil {
			// Include error as a failed check
			results = append(results, &CompatibilityCheck{
				RepoID:          repo.RepoID,
				Path:            repo.Path,
				RequiredVersion: MinSchemaVersion,
				Status:          CompatibilityIncompatible,
				Message:         fmt.Sprintf("Error checking compatibility: %v", err),
			})
			continue
		}
		results = append(results, check)
	}

	return results, nil
}

// AreAllReposCompatible returns true if all repos are compatible
func AreAllReposCompatible(checks []*CompatibilityCheck) bool {
	for _, check := range checks {
		if check.Status != CompatibilityOK {
			return false
		}
	}
	return true
}

// GetIncompatibleRepos returns only the incompatible repos
func GetIncompatibleRepos(checks []*CompatibilityCheck) []*CompatibilityCheck {
	var incompatible []*CompatibilityCheck
	for _, check := range checks {
		if check.Status != CompatibilityOK {
			incompatible = append(incompatible, check)
		}
	}
	return incompatible
}
