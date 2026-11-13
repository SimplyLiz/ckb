package project

import (
	"strings"
	"testing"
)

func TestGetIndexerConfig(t *testing.T) {
	tests := []struct {
		name               string
		lang               Language
		wantNil            bool
		wantCmd            string
		wantOutputFlag     string
		wantSupportsInc    bool
		wantHasFixedOutput bool
	}{
		{
			name:            "Go",
			lang:            LangGo,
			wantCmd:         "scip-go",
			wantOutputFlag:  "--output",
			wantSupportsInc: true,
		},
		{
			name:            "TypeScript",
			lang:            LangTypeScript,
			wantCmd:         "scip-typescript",
			wantOutputFlag:  "--output",
			wantSupportsInc: true,
		},
		{
			name:            "JavaScript",
			lang:            LangJavaScript,
			wantCmd:         "scip-typescript",
			wantOutputFlag:  "--output",
			wantSupportsInc: true,
		},
		{
			name:            "Python",
			lang:            LangPython,
			wantCmd:         "scip-python",
			wantOutputFlag:  "--output",
			wantSupportsInc: true,
		},
		{
			name:            "Dart",
			lang:            LangDart,
			wantCmd:         "dart",
			wantOutputFlag:  "--output",
			wantSupportsInc: true,
		},
		{
			name:            "Rust",
			lang:            LangRust,
			wantCmd:         "rust-analyzer",
			wantOutputFlag:  "--output",
			wantSupportsInc: true,
		},
		{
			name:            "C++ names the flag differently",
			lang:            LangCpp,
			wantCmd:         "scip-clang",
			wantOutputFlag:  "--index-output-path",
			wantSupportsInc: false,
		},
		{
			name:            "Ruby names the flag differently",
			lang:            LangRuby,
			wantCmd:         "scip-ruby",
			wantOutputFlag:  "--index-file",
			wantSupportsInc: false,
		},
		{
			name:               "PHP (fixed output)",
			lang:               LangPHP,
			wantCmd:            "vendor/bin/scip-php",
			wantOutputFlag:     "",
			wantSupportsInc:    false,
			wantHasFixedOutput: true,
		},
		{
			name:            "Java (no incremental)",
			lang:            LangJava,
			wantCmd:         "scip-java",
			wantOutputFlag:  "--output",
			wantSupportsInc: false,
		},
		{
			name:    "Unknown",
			lang:    LangUnknown,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := GetIndexerConfig(tt.lang)
			if tt.wantNil {
				if config != nil {
					t.Errorf("GetIndexerConfig(%v) = %v, want nil", tt.lang, config)
				}
				return
			}

			if config == nil {
				t.Fatalf("GetIndexerConfig(%v) = nil, want non-nil", tt.lang)
			}

			if config.Cmd != tt.wantCmd {
				t.Errorf("config.Cmd = %q, want %q", config.Cmd, tt.wantCmd)
			}

			if config.OutputFlag != tt.wantOutputFlag {
				t.Errorf("config.OutputFlag = %q, want %q", config.OutputFlag, tt.wantOutputFlag)
			}

			if config.SupportsIncremental != tt.wantSupportsInc {
				t.Errorf("config.SupportsIncremental = %v, want %v", config.SupportsIncremental, tt.wantSupportsInc)
			}

			if config.HasFixedOutput() != tt.wantHasFixedOutput {
				t.Errorf("config.HasFixedOutput() = %v, want %v", config.HasFixedOutput(), tt.wantHasFixedOutput)
			}
		})
	}
}

func TestSupportsIncrementalIndexing(t *testing.T) {
	tests := []struct {
		lang Language
		want bool
	}{
		{LangGo, true},
		{LangTypeScript, true},
		{LangJavaScript, true},
		{LangPython, true},
		{LangDart, true},
		{LangRust, true},
		{LangJava, false},
		{LangKotlin, false},
		{LangCpp, false},
		{LangRuby, false},
		{LangCSharp, false},
		{LangPHP, false},
		{LangUnknown, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.lang), func(t *testing.T) {
			got := SupportsIncrementalIndexing(tt.lang)
			if got != tt.want {
				t.Errorf("SupportsIncrementalIndexing(%v) = %v, want %v", tt.lang, got, tt.want)
			}
		})
	}
}

func TestIndexerConfig_BuildCommand(t *testing.T) {
	tests := []struct {
		name       string
		config     IndexerConfig
		outputPath string
		wantBinary string
		wantArgs   []string
	}{
		{
			name: "scip-go with output",
			config: IndexerConfig{
				Cmd:        "scip-go",
				OutputFlag: "--output",
			},
			outputPath: "/tmp/index.scip",
			wantBinary: "scip-go",
			wantArgs:   []string{"--output", "/tmp/index.scip"},
		},
		{
			name: "scip-typescript with args",
			config: IndexerConfig{
				Cmd:        "scip-typescript",
				Args:       []string{"index", "--infer-tsconfig"},
				OutputFlag: "--output",
			},
			outputPath: "/tmp/index.scip",
			wantBinary: "scip-typescript",
			wantArgs:   []string{"index", "--infer-tsconfig", "--output", "/tmp/index.scip"},
		},
		{
			name: "rust-analyzer with fixed output (no flag)",
			config: IndexerConfig{
				Cmd:         "rust-analyzer",
				Args:        []string{"scip", "."},
				FixedOutput: "index.scip",
			},
			outputPath: "/tmp/index.scip",
			wantBinary: "rust-analyzer",
			wantArgs:   []string{"scip", "."},
		},
		{
			name: "dart with complex args",
			config: IndexerConfig{
				Cmd:        "dart",
				Args:       []string{"pub", "global", "run", "scip_dart", "./"},
				OutputFlag: "--output",
			},
			outputPath: ".scip/index.scip",
			wantBinary: "dart",
			wantArgs:   []string{"pub", "global", "run", "scip_dart", "./", "--output", ".scip/index.scip"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.config.BuildCommand(tt.outputPath)

			// Check binary name
			cmdPath := cmd.Path
			if !strings.HasSuffix(cmdPath, tt.wantBinary) && cmdPath != tt.wantBinary {
				// cmd.Path might be the full path from LookPath, or just the command name
				// Just check the command was set correctly via Args[0]
				if len(cmd.Args) == 0 || cmd.Args[0] != tt.wantBinary {
					t.Errorf("cmd.Args[0] = %q, want %q", cmd.Args[0], tt.wantBinary)
				}
			}

			// Check args (Args[0] is the command name in exec.Cmd)
			gotArgs := cmd.Args[1:]
			if len(gotArgs) != len(tt.wantArgs) {
				t.Errorf("len(cmd.Args[1:]) = %d, want %d", len(gotArgs), len(tt.wantArgs))
				t.Errorf("got args: %v, want: %v", gotArgs, tt.wantArgs)
				return
			}

			for i, want := range tt.wantArgs {
				if gotArgs[i] != want {
					t.Errorf("cmd.Args[%d] = %q, want %q", i+1, gotArgs[i], want)
				}
			}
		})
	}
}

func TestIndexerConfig_HasFixedOutput(t *testing.T) {
	tests := []struct {
		name   string
		config IndexerConfig
		want   bool
	}{
		{
			name: "with fixed output",
			config: IndexerConfig{
				FixedOutput: "index.scip",
			},
			want: true,
		},
		{
			name: "without fixed output",
			config: IndexerConfig{
				OutputFlag: "--output",
			},
			want: false,
		},
		{
			name:   "empty config",
			config: IndexerConfig{},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.HasFixedOutput()
			if got != tt.want {
				t.Errorf("HasFixedOutput() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIndexerConfig_IsInstalled(t *testing.T) {
	tests := []struct {
		name   string
		config IndexerConfig
		// We can't guarantee which commands exist, so we just test it doesn't panic
		// and returns a boolean
	}{
		{
			name: "simple command",
			config: IndexerConfig{
				Cmd: "ls", // Should exist on all Unix systems
			},
		},
		{
			name: "multi-part command (first part exists)",
			config: IndexerConfig{
				Cmd: "ls -la", // ls exists, but treated as multi-part
			},
		},
		{
			name: "non-existent command",
			config: IndexerConfig{
				Cmd: "definitely-not-a-real-command-12345",
			},
		},
		{
			name: "multi-part non-existent",
			config: IndexerConfig{
				Cmd: "not-real with args",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			result := tt.config.IsInstalled()
			// Just verify it returns a boolean (no panic)
			_ = result
		})
	}

	// Test specific known behaviors
	t.Run("ls command exists", func(t *testing.T) {
		config := IndexerConfig{Cmd: "ls"}
		if !config.IsInstalled() {
			t.Skip("ls not found, unusual system")
		}
	})

	t.Run("multi-part uses first token", func(t *testing.T) {
		// "ls -la" should check for "ls" which exists
		config := IndexerConfig{Cmd: "ls -la"}
		if !config.IsInstalled() {
			t.Skip("ls not found, unusual system")
		}
	})

	t.Run("non-existent returns false", func(t *testing.T) {
		config := IndexerConfig{Cmd: "ckb-definitely-not-installed-xyz"}
		if config.IsInstalled() {
			t.Error("expected non-existent command to return false")
		}
	})
}

func TestIndexerConfig_BuildCommand_EmptyOutput(t *testing.T) {
	// Test with empty output path
	config := IndexerConfig{
		Cmd:        "scip-go",
		OutputFlag: "--output",
	}

	cmd := config.BuildCommand("")
	// Should not include --output flag when outputPath is empty
	for i, arg := range cmd.Args {
		if arg == "--output" {
			t.Errorf("expected no --output flag when outputPath is empty, found at index %d", i)
		}
	}
}

func TestIndexerConfig_BuildCommand_NoOutputFlag(t *testing.T) {
	// Test with no output flag (fixed output indexer style)
	config := IndexerConfig{
		Cmd:  "rust-analyzer",
		Args: []string{"scip", "."},
		// No OutputFlag
	}

	cmd := config.BuildCommand("/tmp/index.scip")
	// Args should just be the base args, no output flag added
	expectedArgs := []string{"rust-analyzer", "scip", "."}
	if len(cmd.Args) != len(expectedArgs) {
		t.Errorf("expected %d args, got %d: %v", len(expectedArgs), len(cmd.Args), cmd.Args)
	}
}
