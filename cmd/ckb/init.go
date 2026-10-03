package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/repos"

	"github.com/spf13/cobra"
)

var (
	initForce      bool
	initName       string
	initNoActivate bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize CKB configuration",
	Long:  "Creates a .ckb/ directory with default configuration in the current repository root",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "Force reinitialization (removes existing .ckb directory)")
	initCmd.Flags().StringVarP(&initName, "name", "n", "", "Repository name for global registry (default: directory name)")
	initCmd.Flags().BoolVar(&initNoActivate, "no-activate", false, "Don't set as active repository after init")
	rootCmd.AddCommand(initCmd)
}

// initOptions controls runInitCore's behavior, independent of the cobra
// flags that drive the 'ckb init' CLI command. Callers other than the CLI
// (like 'ckb setup', which runs the same init logic internally) build one
// explicitly instead of mutating the package-level init* flag vars, so
// there's no risk of one caller's intent leaking into another's.
type initOptions struct {
	// Force removes and recreates .ckb/ if it already exists.
	Force bool
	// Name is the repository name for the global registry; defaults to the
	// current directory's base name when empty.
	Name string
	// NoActivate registers the repo (if not already registered) without
	// changing the user's global default/active repository.
	NoActivate bool
	// Dir is the directory to initialize; defaults to the current directory
	// when empty.
	Dir string
	// Out receives the human-readable progress text; defaults to os.Stdout.
	// 'ckb mcp' passes io.Discard because its stdout is the JSON-RPC transport.
	Out io.Writer
}

func runInit(cmd *cobra.Command, args []string) error {
	return runInitCore(initOptions{Force: initForce, Name: initName, NoActivate: initNoActivate})
}

func runInitCore(opts initOptions) error {
	logger := newLogger("human")

	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	cwd := opts.Dir
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return errors.NewCkbError(errors.InternalError, "Failed to get current directory", err, nil, nil)
		}
	}

	// Check if .ckb already exists
	ckbDir := filepath.Join(cwd, ".ckb")
	if _, statErr := os.Stat(ckbDir); statErr == nil {
		if !opts.Force {
			// Idempotent behavior: already initialized is success (CI-friendly)
			fmt.Fprintln(out, "CKB already initialized.")
			fmt.Fprintf(out, "Configuration at: %s\n", filepath.Join(ckbDir, "config.json"))
			fmt.Fprintln(out, "\nRun 'ckb init --force' to reinitialize.")
			return nil
		}
		// Remove existing directory
		if removeErr := os.RemoveAll(ckbDir); removeErr != nil {
			return errors.NewCkbError(errors.InternalError, "Failed to remove existing .ckb directory", removeErr, nil, nil)
		}
		logger.Info("Removed existing .ckb directory")
	}

	// Create .ckb directory
	if mkdirErr := os.MkdirAll(ckbDir, 0755); mkdirErr != nil {
		return errors.NewCkbError(errors.InternalError, "Failed to create .ckb directory", mkdirErr, nil, nil)
	}

	// Create default config
	cfg := config.DefaultConfig()
	cfg.RepoRoot = "."

	// Write config file
	configPath := filepath.Join(ckbDir, "config.json")
	configData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return errors.NewCkbError(errors.InternalError, "Failed to marshal config", err, nil, nil)
	}

	if writeErr := os.WriteFile(configPath, configData, 0644); writeErr != nil {
		return errors.NewCkbError(errors.InternalError, "Failed to write config file", writeErr, nil, nil)
	}

	logger.Info("CKB initialized successfully", "config_path", configPath)

	// Register in global registry
	repoName := opts.Name
	if repoName == "" {
		repoName = filepath.Base(cwd)
	}

	registry, err := repos.LoadRegistry()
	if err != nil {
		// Non-fatal: warn but continue
		logger.Warn("Failed to load global registry", "error", err.Error())
	} else {
		// Check if already registered (possibly under different name)
		existingEntry, _ := registry.GetByPath(cwd)
		if existingEntry != nil {
			logger.Info("Repository already registered", "name", existingEntry.Name)
			repoName = existingEntry.Name
		} else {
			// Check if name is taken
			if _, _, err := registry.Get(repoName); err == nil {
				// Name exists, try to find unique name
				baseName := repoName
				for i := 2; i <= 99; i++ {
					candidate := fmt.Sprintf("%s-%d", baseName, i)
					if _, _, err := registry.Get(candidate); err != nil {
						repoName = candidate
						break
					}
				}
			}

			// Register the repo
			if err := registry.Add(repoName, cwd); err != nil {
				logger.Warn("Failed to register in global registry", "error", err.Error())
			} else {
				logger.Info("Registered in global registry", "name", repoName)
			}
		}

		// Set as active unless the caller asked not to
		if !opts.NoActivate {
			if err := registry.SetDefault(repoName); err != nil {
				logger.Warn("Failed to set as active repository", "error", err.Error())
			}
		}
	}

	fmt.Fprintln(out, "CKB initialized successfully!")
	fmt.Fprintf(out, "Configuration written to: %s\n", configPath)
	fmt.Fprintf(out, "Registered as: %s\n", repoName)
	if !opts.NoActivate {
		fmt.Fprintf(out, "Active repository: %s\n", repoName)
	}
	fmt.Fprintln(out, "\nNext steps:")
	fmt.Fprintln(out, "  1. Run 'ckb index' to create SCIP index")
	fmt.Fprintln(out, "  2. Run 'ckb doctor' to check your setup")
	fmt.Fprintln(out, "  3. Run 'ckb status' to see system status")

	return nil
}
