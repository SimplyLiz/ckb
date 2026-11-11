package deadcode

import (
	"path/filepath"
	"strings"
)

// ExclusionRules determines which symbols should be excluded from dead code analysis.
type ExclusionRules struct {
	patterns []string
}

// NewExclusionRules creates exclusion rules with the given patterns.
func NewExclusionRules(patterns []string) *ExclusionRules {
	return &ExclusionRules{
		patterns: patterns,
	}
}

// SymbolInfo contains information about a symbol for exclusion checking.
type SymbolInfo struct {
	SymbolID      string
	Name          string
	Kind          string
	FilePath      string
	LineNumber    int
	Signature     string
	Documentation string
	Tags          string // Struct field tags
	Exported      bool
}

// ShouldExclude returns a reason if the symbol should be excluded, or empty string if not.
func (r *ExclusionRules) ShouldExclude(sym SymbolInfo) string {
	// 1. Main and init functions are entry points
	if sym.Name == "main" || sym.Name == "init" {
		return "entry point function"
	}

	// 2. Test functions and benchmarks
	if strings.HasPrefix(sym.Name, "Test") || strings.HasPrefix(sym.Name, "Benchmark") {
		return "test or benchmark function"
	}

	// 3. Example functions (for godoc)
	if strings.HasPrefix(sym.Name, "Example") {
		return "example function for documentation"
	}

	// 4. Interface implementation markers (common pattern: var _ Interface = (*Type)(nil))
	if strings.HasPrefix(sym.Name, "_") && sym.Kind == "variable" {
		return "interface implementation check"
	}

	// 5. Serialization fields (JSON, XML, YAML, etc.)
	if sym.Kind == "field" && hasSerializationTag(sym.Tags) {
		return "has serialization tag"
	}

	// 6. CGo exports
	if strings.Contains(sym.Documentation, "//export ") {
		return "CGo export directive"
	}

	// 7. Common interface implementations
	if isCommonInterfaceMethod(sym.Name, sym.Kind) {
		return "common interface implementation"
	}

	// 8. Generated file patterns
	if isGeneratedFile(sym.FilePath) {
		return "generated file"
	}

	// 9. User-defined exclusion patterns
	for _, pattern := range r.patterns {
		// Try matching against file path
		if matched, _ := filepath.Match(pattern, sym.FilePath); matched {
			return "matches exclusion pattern: " + pattern
		}
		// Try matching against symbol name
		if matched, _ := filepath.Match(pattern, sym.Name); matched {
			return "matches exclusion pattern: " + pattern
		}
		// Handle ** patterns with simple contains check
		if strings.Contains(pattern, "**") {
			simplified := strings.ReplaceAll(pattern, "**", "")
			simplified = strings.ReplaceAll(simplified, "*", "")
			if simplified != "" && strings.Contains(sym.FilePath, simplified) {
				return "matches exclusion pattern: " + pattern
			}
		}
	}

	return ""
}

// hasSerializationTag checks if a struct field has serialization tags.
func hasSerializationTag(tags string) bool {
	if tags == "" {
		return false
	}

	serializationTags := []string{
		`json:"`,
		`xml:"`,
		`yaml:"`,
		`toml:"`,
		`mapstructure:"`,
		`bson:"`,
		`protobuf:"`,
		`msgpack:"`,
		`db:"`,
		`sql:"`,
		`gorm:"`,
		`validate:"`,
		`binding:"`,
		`form:"`,
		`query:"`,
		`param:"`,
		`header:"`,
	}

	for _, tag := range serializationTags {
		if strings.Contains(tags, tag) {
			return true
		}
	}
	return false
}

// isCommonInterfaceMethod checks if a method is a common interface implementation.
func isCommonInterfaceMethod(name, kind string) bool {
	if kind != "method" {
		return false
	}

	// Common Go interfaces that are often implemented
	commonMethods := map[string]bool{
		// fmt.Stringer
		"String": true,
		// error interface
		"Error": true,
		// io.Reader/Writer/Closer
		"Read":  true,
		"Write": true,
		"Close": true,
		// io.Seeker
		"Seek": true,
		// sort.Interface
		"Len":  true,
		"Less": true,
		"Swap": true,
		// encoding.TextMarshaler/Unmarshaler
		"MarshalText":   true,
		"UnmarshalText": true,
		// encoding.BinaryMarshaler/Unmarshaler
		"MarshalBinary":   true,
		"UnmarshalBinary": true,
		// json.Marshaler/Unmarshaler
		"MarshalJSON":   true,
		"UnmarshalJSON": true,
		// sql.Scanner/driver.Valuer
		"Scan":  true,
		"Value": true,
		// context.Context methods
		"Deadline": true,
		"Done":     true,
		"Err":      true,
		// http.Handler
		"ServeHTTP": true,
		// flag.Value
		"Set": true,
		// driver interfaces
		"Open":    true,
		"Prepare": true,
		"Begin":   true,
		"Commit":  true,
		// gRPC interfaces
		"Invoke":    true,
		"NewStream": true,
	}

	return commonMethods[name]
}

// isGeneratedFile checks if a file is likely generated.
func isGeneratedFile(path string) bool {
	// Common generated file patterns
	generatedPatterns := []string{
		"_generated.go",
		"_gen.go",
		".pb.go",
		".pb.gw.go",
		"_string.go",
		"_enumer.go",
		"mock_",
		"mocks/",
		"generated/",
		"zz_generated",
		"_easyjson.go",
		"_ffjson.go",
		"bindata.go",
		"wire_gen.go",
		"ent/",
		"sqlc/",
	}

	pathLower := strings.ToLower(path)
	for _, pattern := range generatedPatterns {
		if strings.Contains(pathLower, pattern) {
			return true
		}
	}

	return false
}

// IsTestFile checks if a file path is a test file.
func IsTestFile(path string) bool {
	// Go tests
	if strings.HasSuffix(path, "_test.go") {
		return true
	}

	// TypeScript/JavaScript tests: name.test.ts, name.spec.tsx, name.test.mjs …
	base := filepath.Base(path)
	if i := strings.LastIndex(base, "."); i > 0 {
		switch stem := base[:i]; {
		case strings.HasSuffix(stem, ".test"), strings.HasSuffix(stem, ".spec"):
			return true
		}
	}

	// Python tests
	if strings.HasSuffix(path, "_test.py") || strings.HasPrefix(filepath.Base(path), "test_") {
		return true
	}

	// Test directories, at the root as well (tests/support/db.ts)
	slashed := "/" + path
	if strings.Contains(slashed, "/test/") ||
		strings.Contains(slashed, "/tests/") ||
		strings.Contains(slashed, "/testdata/") ||
		strings.Contains(slashed, "/__tests__/") {
		return true
	}

	return false
}

// IsHiddenPath reports whether a path component starts with a dot: .next/,
// .venv/, .cache/. What lives there is build output or someone else's code.
func IsHiddenPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if len(part) > 1 && part[0] == '.' {
			return true
		}
	}
	return false
}
