// Package project provides language and indexer detection for repositories.
package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IndexerConfig defines how to run a SCIP indexer for a language.
type IndexerConfig struct {
	// Cmd is the base command (e.g., "scip-go", "scip-typescript")
	Cmd string

	// Args are additional arguments before the output flag
	Args []string

	// OutputFlag is the flag for specifying output path (e.g., "--output")
	// Empty if the indexer uses a fixed output path
	OutputFlag string

	// FixedOutput is the fixed output filename for indexers that ignore --output
	// (e.g., rust-analyzer always outputs "index.scip")
	FixedOutput string

	// SupportsIncremental indicates if this indexer works with incremental mode
	SupportsIncremental bool

	// Extensions are the source files the indexer reads. Incremental change
	// detection looks at these and nothing else, so a language without them
	// would never see a change and never re-run its indexer.
	Extensions []string
}

var tsExtensions = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}

// Indexers maps languages to their SCIP indexer configurations.
// Only languages with SupportsIncremental=true can use incremental indexing.
var Indexers = map[Language]IndexerConfig{
	LangGo: {
		Cmd:                 "scip-go",
		OutputFlag:          "--output",
		SupportsIncremental: true,
		Extensions:          []string{".go"},
	},
	LangTypeScript: {
		Cmd:                 "scip-typescript",
		Args:                []string{"index", "--infer-tsconfig"},
		OutputFlag:          "--output",
		SupportsIncremental: true,
		Extensions:          tsExtensions,
	},
	LangJavaScript: {
		Cmd:                 "scip-typescript",
		Args:                []string{"index", "--infer-tsconfig"},
		OutputFlag:          "--output",
		SupportsIncremental: true,
		Extensions:          tsExtensions,
	},
	LangPython: {
		Cmd:                 "scip-python",
		Args:                []string{"index", "."},
		OutputFlag:          "--output",
		SupportsIncremental: true,
		Extensions:          []string{".py", ".pyi"},
	},
	LangDart: {
		Cmd:                 "dart",
		Args:                []string{"pub", "global", "run", "scip_dart", "./"},
		OutputFlag:          "--output",
		SupportsIncremental: true,
		Extensions:          []string{".dart"},
	},
	LangRust: {
		Cmd:                 "rust-analyzer",
		Args:                []string{"scip", "."},
		FixedOutput:         "index.scip",
		SupportsIncremental: true,
		Extensions:          []string{".rs"},
	},
	LangJava: {
		Cmd:                 "scip-java",
		Args:                []string{"index"},
		OutputFlag:          "--output",
		SupportsIncremental: false, // Build system complexity
	},
	LangKotlin: {
		Cmd:                 "scip-java",
		Args:                []string{"index"},
		OutputFlag:          "--output",
		SupportsIncremental: false, // Build system complexity
	},
	LangCpp: {
		Cmd:                 "scip-clang",
		OutputFlag:          "--output",
		SupportsIncremental: false, // Requires compile_commands.json
	},
	LangRuby: {
		Cmd:                 "scip-ruby",
		Args:                []string{"."},
		OutputFlag:          "--output",
		SupportsIncremental: false, // Lower priority
	},
	LangCSharp: {
		Cmd:                 "scip-dotnet",
		Args:                []string{"index"},
		OutputFlag:          "--output",
		SupportsIncremental: false, // Lower priority
	},
	LangPHP: {
		Cmd:                 "vendor/bin/scip-php",
		OutputFlag:          "--output",
		SupportsIncremental: false, // Lower priority
	},
}

// GetIndexerConfig returns the indexer configuration for a language.
// Returns nil if no indexer is configured for the language.
func GetIndexerConfig(lang Language) *IndexerConfig {
	config, ok := Indexers[lang]
	if !ok {
		return nil
	}
	return &config
}

// SupportsIncrementalIndexing returns true if the language supports incremental indexing.
func SupportsIncrementalIndexing(lang Language) bool {
	config := GetIndexerConfig(lang)
	if config == nil {
		return false
	}
	return config.SupportsIncremental
}

// BuildCommand creates an exec.Cmd for running the indexer with the given output path.
func (c *IndexerConfig) BuildCommand(outputPath string) *exec.Cmd {
	args := make([]string, 0, len(c.Args)+2)
	args = append(args, c.Args...)

	// Add output flag if supported (not for fixed output indexers)
	if c.OutputFlag != "" && outputPath != "" {
		args = append(args, c.OutputFlag, outputPath)
	}

	// Resolve the command path (check PATH and ~/go/bin)
	cmdPath := c.resolveCmdPath()
	return exec.Command(cmdPath, args...) // #nosec G204 //nolint:gosec // command from trusted indexer config
}

// resolveCmdPath finds the full path to the indexer command.
func (c *IndexerConfig) resolveCmdPath() string {
	cmd := c.Cmd
	if strings.Contains(cmd, " ") {
		parts := strings.Fields(cmd)
		cmd = parts[0]
	}

	// Check standard PATH first
	if path, err := exec.LookPath(cmd); err == nil {
		return path
	}

	// Check ~/go/bin for Go-installed tools
	if home, err := os.UserHomeDir(); err == nil {
		goBinPath := filepath.Join(home, "go", "bin", cmd)
		if _, err := os.Stat(goBinPath); err == nil {
			return goBinPath
		}
	}

	// Return original command and let it fail with a clear error
	return c.Cmd
}

// IsInstalled checks if the indexer command is available in PATH or common locations.
func (c *IndexerConfig) IsInstalled() bool {
	// For multi-part commands like "dart pub global run scip_dart",
	// we just check if the base command exists
	cmd := c.Cmd
	if strings.Contains(cmd, " ") {
		parts := strings.Fields(cmd)
		cmd = parts[0]
	}

	// Check standard PATH first
	if _, err := exec.LookPath(cmd); err == nil {
		return true
	}

	// Check ~/go/bin for Go-installed tools (scip-go, etc.)
	if home, err := os.UserHomeDir(); err == nil {
		goBinPath := filepath.Join(home, "go", "bin", cmd)
		if _, err := os.Stat(goBinPath); err == nil {
			return true
		}
	}

	return false
}

// HasFixedOutput returns true if the indexer outputs to a fixed path.
func (c *IndexerConfig) HasFixedOutput() bool {
	return c.FixedOutput != ""
}

// UndoInferredTSConfig returns what removes the tsconfig.json that
// scip-typescript's --infer-tsconfig writes into dir when dir has none, and
// leaves behind: an untracked file in the user's repo after every index. A
// tsconfig.json that was there before is never touched; a command without the
// flag gets a no-op. Call it before the indexer runs, the result after.
func UndoInferredTSConfig(dir, command string) func() {
	if !strings.Contains(command, "--infer-tsconfig") {
		return func() {}
	}
	path := filepath.Join(dir, "tsconfig.json")
	if _, err := os.Lstat(path); err == nil {
		return func() {}
	}
	return func() { _ = os.Remove(path) }
}
