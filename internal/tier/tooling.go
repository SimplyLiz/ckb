package tier

import (
	"runtime"
	"strings"
)

// Language represents a programming language for tier detection.
type Language string

const (
	LangGo         Language = "go"
	LangTypeScript Language = "typescript"
	LangJavaScript Language = "javascript"
	LangPython     Language = "python"
	LangRust       Language = "rust"
	LangJava       Language = "java"
	LangKotlin     Language = "kotlin"
	LangCpp        Language = "cpp"
	LangCSharp     Language = "csharp"
	LangRuby       Language = "ruby"
	LangDart       Language = "dart"
	LangPHP        Language = "php"
)

// String returns the language name.
func (l Language) String() string {
	return string(l)
}

// DisplayName returns a human-readable language name.
func (l Language) DisplayName() string {
	switch l {
	case LangGo:
		return "Go"
	case LangTypeScript:
		return "TypeScript"
	case LangJavaScript:
		return "JavaScript"
	case LangPython:
		return "Python"
	case LangRust:
		return "Rust"
	case LangJava:
		return "Java"
	case LangKotlin:
		return "Kotlin"
	case LangCpp:
		return "C/C++"
	case LangCSharp:
		return "C#"
	case LangRuby:
		return "Ruby"
	case LangDart:
		return "Dart"
	case LangPHP:
		return "PHP"
	default:
		return string(l)
	}
}

// AllLanguages returns all supported languages in alphabetical order.
func AllLanguages() []Language {
	return []Language{
		LangCpp,
		LangCSharp,
		LangDart,
		LangGo,
		LangJava,
		LangJavaScript,
		LangKotlin,
		LangPHP,
		LangPython,
		LangRuby,
		LangRust,
		LangTypeScript,
	}
}

// ParseLanguage parses a language string.
func ParseLanguage(s string) (Language, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "go", "golang":
		return LangGo, true
	case "typescript", "ts":
		return LangTypeScript, true
	case "javascript", "js":
		return LangJavaScript, true
	case "python", "py":
		return LangPython, true
	case "rust", "rs":
		return LangRust, true
	case "java":
		return LangJava, true
	case "kotlin", "kt":
		return LangKotlin, true
	case "cpp", "c++", "cxx", "c":
		return LangCpp, true
	case "csharp", "c#", "cs":
		return LangCSharp, true
	case "ruby", "rb":
		return LangRuby, true
	case "dart":
		return LangDart, true
	case "php":
		return LangPHP, true
	default:
		return "", false
	}
}

// Capability represents a code intelligence capability.
type Capability string

const (
	CapDefinitions     Capability = "definitions"
	CapReferences      Capability = "references"
	CapCallGraph       Capability = "callgraph"
	CapHover           Capability = "hover"
	CapTypeInfo        Capability = "type-info"
	CapRename          Capability = "rename"
	CapWorkspaceSymbol Capability = "workspace-symbols"
)

// String returns the capability name.
func (c Capability) String() string {
	return string(c)
}

// AllCapabilities returns all capabilities in display order.
func AllCapabilities() []Capability {
	return []Capability{
		CapDefinitions,
		CapReferences,
		CapCallGraph,
		CapTypeInfo,
		CapHover,
		CapRename,
		CapWorkspaceSymbol,
	}
}

// Provider represents a backend that can provide capabilities.
type Provider string

const (
	ProviderSCIP       Provider = "scip"
	ProviderLSP        Provider = "lsp"
	ProviderTreeSitter Provider = "tree-sitter"
	ProviderHeuristic  Provider = "heuristic"
)

// String returns the provider name.
func (p Provider) String() string {
	return string(p)
}

// TierCapabilities maps each tier to its guaranteed capabilities.
var TierCapabilities = map[AnalysisTier][]Capability{
	TierBasic:    {CapDefinitions},
	TierEnhanced: {CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
	TierFull:     {CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo, CapHover, CapRename, CapWorkspaceSymbol},
}

// CapabilityProviders maps capabilities to acceptable providers (in preference order).
var CapabilityProviders = map[Capability][]Provider{
	CapDefinitions:     {ProviderSCIP, ProviderLSP, ProviderTreeSitter},
	CapReferences:      {ProviderSCIP, ProviderLSP},
	CapCallGraph:       {ProviderSCIP, ProviderHeuristic},
	CapHover:           {ProviderLSP},
	CapTypeInfo:        {ProviderSCIP, ProviderLSP},
	CapRename:          {ProviderLSP},
	CapWorkspaceSymbol: {ProviderLSP},
}

// IndexerRequirement defines an external tool needed to achieve a tier.
// This is distinct from ToolRequirement which defines MCP tool tier gating.
type IndexerRequirement struct {
	// Name is the tool name (for display).
	Name string

	// Binary is the executable name to look for in PATH.
	Binary string

	// VersionArgs are arguments to get version (e.g., ["--version"]).
	VersionArgs []string

	// MinVersion is the minimum required version (semver, empty = any).
	MinVersion string

	// InstallCmd is the installation command per OS.
	// Keys: "darwin", "linux", "windows", "default"
	InstallCmd map[string]string

	// Provider indicates what this tool provides.
	Provider Provider

	// Capabilities lists what capabilities this tool enables.
	Capabilities []Capability
}

// GetInstallCommand returns the install command for the current OS.
func (t IndexerRequirement) GetInstallCommand() string {
	if cmd, ok := t.InstallCmd[runtime.GOOS]; ok {
		return cmd
	}
	if cmd, ok := t.InstallCmd["default"]; ok {
		return cmd
	}
	return ""
}

// LanguageRequirements maps languages to tier requirements.
var LanguageRequirements = map[Language]map[AnalysisTier][]IndexerRequirement{
	LangGo: {
		TierEnhanced: {
			{
				Name:        "scip-go",
				Binary:      "scip-go",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "go install github.com/sourcegraph/scip-go/cmd/scip-go@latest",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "gopls",
				Binary:      "gopls",
				VersionArgs: []string{"version"},
				MinVersion:  "0.15.0",
				InstallCmd: map[string]string{
					"default": "go install golang.org/x/tools/gopls@latest",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangTypeScript: {
		TierEnhanced: {
			{
				Name:        "scip-typescript",
				Binary:      "scip-typescript",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "npm install -g @sourcegraph/scip-typescript",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "typescript-language-server",
				Binary:      "typescript-language-server",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "npm install -g typescript-language-server typescript",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangJavaScript: {
		TierEnhanced: {
			{
				Name:        "scip-typescript",
				Binary:      "scip-typescript",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "npm install -g @sourcegraph/scip-typescript",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "typescript-language-server",
				Binary:      "typescript-language-server",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "npm install -g typescript-language-server typescript",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangPython: {
		TierEnhanced: {
			{
				Name:        "scip-python",
				Binary:      "scip-python",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "npm install -g @sourcegraph/scip-python",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "pylsp",
				Binary:      "pylsp",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "pip install python-lsp-server",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangRust: {
		TierEnhanced: {
			{
				Name:        "rust-analyzer",
				Binary:      "rust-analyzer",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "rustup component add rust-analyzer",
				},
				Provider:     ProviderSCIP, // rust-analyzer can generate SCIP
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {}, // rust-analyzer provides both SCIP and LSP
	},
	LangJava: {
		TierEnhanced: {
			{
				Name:        "scip-java",
				Binary:      "scip-java",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"darwin":  "brew install coursier/formulas/coursier && cs install scip-java",
					"linux":   "curl -fL https://github.com/coursier/coursier/releases/download/v2.1.8/cs-x86_64-pc-linux.gz | gzip -d > cs && chmod +x cs && ./cs install scip-java",
					"default": "cs install scip-java",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "jdtls",
				Binary:      "jdtls",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"darwin":  "brew install jdtls",
					"default": "# See https://github.com/eclipse-jdtls/eclipse.jdt.ls",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangKotlin: {
		TierEnhanced: {
			{
				Name:        "scip-java", // scip-java handles Kotlin via Gradle plugin
				Binary:      "scip-java",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"darwin":  "brew install coursier/formulas/coursier && cs install scip-java",
					"default": "cs install scip-java",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "kotlin-language-server",
				Binary:      "kotlin-language-server",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"darwin":  "brew install kotlin-language-server",
					"default": "# See https://github.com/fwcd/kotlin-language-server",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangCpp: {
		TierEnhanced: {
			{
				Name:        "scip-clang",
				Binary:      "scip-clang",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"darwin":  "brew install sourcegraph/scip-clang/scip-clang",
					"linux":   "# Download from https://github.com/sourcegraph/scip-clang/releases",
					"default": "# See https://github.com/sourcegraph/scip-clang",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "clangd",
				Binary:      "clangd",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"darwin":  "brew install llvm",
					"linux":   "apt install clangd",
					"default": "# Install LLVM toolchain",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangCSharp: {
		TierEnhanced: {
			{
				Name:        "scip-dotnet",
				Binary:      "scip-dotnet",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "dotnet tool install --global scip-dotnet",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "OmniSharp",
				Binary:      "OmniSharp",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"darwin":  "brew install omnisharp/omnisharp-roslyn/omnisharp-mono",
					"default": "# See https://github.com/OmniSharp/omnisharp-roslyn",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangRuby: {
		TierEnhanced: {
			{
				Name:        "scip-ruby",
				Binary:      "scip-ruby",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "# Download from https://github.com/sourcegraph/scip-ruby/releases",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "solargraph",
				Binary:      "solargraph",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "gem install solargraph",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangDart: {
		TierEnhanced: {
			{
				Name:        "scip_dart",
				Binary:      "scip_dart",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "dart pub global activate scip_dart",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "dart",
				Binary:      "dart",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "# Dart SDK includes LSP server",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
	LangPHP: {
		TierEnhanced: {
			{
				Name:        "scip-php",
				Binary:      "vendor/bin/scip-php",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "composer require --dev davidrjenni/scip-php",
				},
				Provider:     ProviderSCIP,
				Capabilities: []Capability{CapDefinitions, CapReferences, CapCallGraph, CapTypeInfo},
			},
		},
		TierFull: {
			{
				Name:        "phpactor",
				Binary:      "phpactor",
				VersionArgs: []string{"--version"},
				InstallCmd: map[string]string{
					"default": "composer global require phpactor/phpactor",
				},
				Provider:     ProviderLSP,
				Capabilities: []Capability{CapHover, CapRename, CapWorkspaceSymbol},
			},
		},
	},
}

// GetIndexerRequirements returns indexer requirements for a language and tier.
func GetIndexerRequirements(lang Language, tier AnalysisTier) []IndexerRequirement {
	if langReqs, ok := LanguageRequirements[lang]; ok {
		if tierReqs, ok := langReqs[tier]; ok {
			return tierReqs
		}
	}
	return nil
}

// GetAllIndexerRequirementsForTier returns all indexer requirements needed to achieve a tier.
// For Full tier, this includes Enhanced tier requirements.
func GetAllIndexerRequirementsForTier(lang Language, tier AnalysisTier) []IndexerRequirement {
	var reqs []IndexerRequirement

	// Enhanced tier is prerequisite for Full
	if tier >= TierEnhanced {
		reqs = append(reqs, GetIndexerRequirements(lang, TierEnhanced)...)
	}
	if tier >= TierFull {
		reqs = append(reqs, GetIndexerRequirements(lang, TierFull)...)
	}

	return reqs
}
