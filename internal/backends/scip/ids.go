package scip

import (
	"fmt"
	"strings"
)

// SCIPIdentifier represents a parsed SCIP symbol identifier
// SCIP format: <scheme> <manager> <package> <descriptor>
// Example: scip-typescript npm @types/node 18.0.0 process.
type SCIPIdentifier struct {
	// Scheme is the indexer scheme (e.g., "scip-typescript", "scip-go")
	Scheme string

	// Manager is the package manager (e.g., "npm", "go", "maven")
	Manager string

	// Package is the package name
	Package string

	// Descriptor is the symbol descriptor path
	Descriptor string

	// Raw is the original SCIP identifier
	Raw string
}

// ParseSCIPIdentifier parses a SCIP symbol identifier
// SCIP identifiers follow the format:
// <scheme> <package-manager> <package-name> <package-version> <descriptor>
//
// Examples:
//
//	scip-typescript npm @types/node 18.0.0 process.
//	scip-go gomod ckb a6af7cfb2eff `ckb/internal/api`/NewServer().
//	scip-java maven com.google.guava guava 31.0 ImmutableList#
func ParseSCIPIdentifier(id string) (*SCIPIdentifier, error) {
	if id == "" {
		return nil, fmt.Errorf("empty SCIP identifier")
	}

	// SCIP identifiers use specific separators
	// The format is: scheme <space> manager <space> package <space> version <space> descriptor
	// But the descriptor can contain spaces, so we need to be careful

	parts := strings.SplitN(id, " ", 5)
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid SCIP identifier format: %s", id)
	}

	result := &SCIPIdentifier{
		Scheme:  parts[0],
		Manager: parts[1],
		Package: parts[2],
		Raw:     id,
	}

	// Handle 4 parts (no version) vs 5 parts (with version)
	if len(parts) == 4 {
		result.Descriptor = parts[3]
	} else {
		// parts[3] is version, parts[4] is descriptor
		result.Descriptor = parts[4]
	}

	return result, nil
}

// GetLanguage extracts the language from the SCIP scheme
// Examples: "scip-typescript" -> "typescript", "scip-go" -> "go"
func (s *SCIPIdentifier) GetLanguage() string {
	if strings.HasPrefix(s.Scheme, "scip-") {
		return s.Scheme[5:]
	}
	return s.Scheme
}

// GetQualifiedName returns a human-readable qualified name
// Example: "@types/node.process"
func (s *SCIPIdentifier) GetQualifiedName() string {
	return fmt.Sprintf("%s.%s", s.Package, s.Descriptor)
}

// GetSimpleName extracts the simple name from the descriptor
// Examples:
//   - "process.env.NODE_ENV." -> "NODE_ENV"
//   - "`ckb/internal/api`/NewServer()." -> "NewServer"
//   - "`ckb/internal/api`/Server#" -> "Server"
//   - "`ckb/internal/api`/Server#Method()." -> "Method"
func (s *SCIPIdentifier) GetSimpleName() string {
	descriptor := s.Descriptor

	// Remove trailing '.' or '#'
	descriptor = strings.TrimSuffix(descriptor, ".")
	descriptor = strings.TrimSuffix(descriptor, "#")

	// Handle scip-go format with backtick-quoted package paths
	// Format: `package/path`/Symbol() or `package/path`/Type or
	// `package/path`/Type#Method() (a method on a type)
	if strings.Contains(descriptor, "`") {
		// Find the last `/` after any backtick-quoted section
		lastBacktick := strings.LastIndex(descriptor, "`")
		if lastBacktick != -1 && lastBacktick < len(descriptor)-1 {
			remainder := descriptor[lastBacktick+1:]
			if idx := strings.LastIndex(remainder, "/"); idx != -1 {
				name := remainder[idx+1:]
				// Remove function parentheses
				name = strings.TrimSuffix(name, "()")
				// A method descriptor still carries its receiver type as
				// "Type#Method" here (the '#' separates them, not '/') —
				// strip it so callers see the method name alone, not the
				// (possibly exported) type name that precedes it.
				if hashIdx := strings.LastIndex(name, "#"); hashIdx != -1 {
					name = name[hashIdx+1:]
				}
				return name
			}
		}
	}

	// Handle standard format with '.' or '/' separators
	// Try '/' first (common in Go)
	if idx := strings.LastIndex(descriptor, "/"); idx != -1 {
		name := descriptor[idx+1:]
		name = strings.TrimSuffix(name, "()")
		return name
	}

	// Fall back to '.' separator
	parts := strings.Split(descriptor, ".")
	if len(parts) == 0 {
		return descriptor
	}
	name := parts[len(parts)-1]
	name = strings.TrimSuffix(name, "()")
	return name
}

// GetContainerName extracts the container name from the descriptor
// Example: "process.env.NODE_ENV." -> "process.env"
func (s *SCIPIdentifier) GetContainerName() string {
	descriptor := strings.TrimSuffix(s.Descriptor, ".")
	parts := strings.Split(descriptor, ".")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], ".")
}

// IsLocal returns true if this is a local (non-external) symbol
func (s *SCIPIdentifier) IsLocal() bool {
	// Local symbols typically have empty package names or use special markers
	return s.Package == "" || s.Package == "."
}

// IsExternal returns true if this is an external dependency symbol
func (s *SCIPIdentifier) IsExternal() bool {
	return !s.IsLocal()
}

// GetStableID returns the stable ID suitable for CKB's symbol tracking
// This normalizes the SCIP ID into a stable format
func (s *SCIPIdentifier) GetStableID() string {
	// Use the raw SCIP ID as the stable ID - it's already stable by design
	return s.Raw
}

// ExtractSymbolKind attempts to infer the symbol kind from the descriptor
// SCIP descriptors include markers:
// - '(' for methods/functions
// - '#' for types/classes
// - '.' for properties/fields
// - '!' for macros
// - etc.
func (s *SCIPIdentifier) ExtractSymbolKind() SymbolKind {
	descriptor := s.Descriptor

	if descriptor == "" {
		return KindUnknown
	}

	hasParens := strings.Contains(descriptor, "(")
	hasReceiver := strings.Contains(descriptor, "#")

	// A descriptor with both '#' and '(' is a method on a receiver type,
	// e.g. `pkg`/Engine#buildProvenance(). — check this combination
	// before the parens-only branch below, otherwise every method gets
	// classified as a bare function (the '#' check never runs, since
	// the '(' check unconditionally returns first).
	if hasParens && hasReceiver {
		return KindMethod
	}

	// Check for function/method (contains '(')
	if hasParens {
		return KindFunction
	}

	// Check for type/class (contains '#')
	if hasReceiver {
		return KindClass
	}

	// Check for constant (all uppercase)
	simpleName := s.GetSimpleName()
	if simpleName == strings.ToUpper(simpleName) && len(simpleName) > 1 {
		return KindConstant
	}

	// Default to property/field
	return KindProperty
}

// NormalizeDescriptor normalizes a SCIP descriptor for comparison
func NormalizeDescriptor(descriptor string) string {
	// Trim trailing dots
	descriptor = strings.TrimSuffix(descriptor, ".")

	// Normalize separators
	descriptor = strings.ReplaceAll(descriptor, "/", ".")
	descriptor = strings.ReplaceAll(descriptor, "::", ".")

	return descriptor
}

// IsMethodDescriptor checks if a descriptor represents a method
func IsMethodDescriptor(descriptor string) bool {
	return strings.Contains(descriptor, "(") && strings.Contains(descriptor, ")")
}

// IsTypeDescriptor checks if a descriptor represents a type
func IsTypeDescriptor(descriptor string) bool {
	return strings.Contains(descriptor, "#")
}

// ExtractPackageInfo extracts package information from a SCIP identifier
type PackageInfo struct {
	Manager string
	Name    string
	Version string
}

// GetPackageInfo extracts package information from the SCIP identifier
func (s *SCIPIdentifier) GetPackageInfo() *PackageInfo {
	// Package field may contain version information
	// Format varies by manager:
	// npm: "@types/node 18.0.0"
	// go: "golang.org/x/tools/go/packages"
	// maven: "com.google.guava guava 31.0"

	packageParts := strings.Fields(s.Package)
	if len(packageParts) == 0 {
		return &PackageInfo{
			Manager: s.Manager,
			Name:    s.Package,
			Version: "",
		}
	}

	// For npm-style packages with version
	if len(packageParts) >= 2 {
		return &PackageInfo{
			Manager: s.Manager,
			Name:    packageParts[0],
			Version: packageParts[len(packageParts)-1],
		}
	}

	return &PackageInfo{
		Manager: s.Manager,
		Name:    s.Package,
		Version: "",
	}
}

// CompareIdentifiers compares two SCIP identifiers for equality
// Returns true if they represent the same symbol
func CompareIdentifiers(id1, id2 string) bool {
	// Direct comparison is sufficient since SCIP IDs are stable
	return id1 == id2
}

// IsValidSCIPIdentifier checks if a string is a valid SCIP identifier
func IsValidSCIPIdentifier(id string) bool {
	if id == "" {
		return false
	}

	// Must start with a scheme (typically "scip-")
	if !strings.HasPrefix(id, "scip-") && !strings.HasPrefix(id, "local") {
		return false
	}

	// Must have at least scheme, manager, package, and descriptor
	parts := strings.SplitN(id, " ", 4)
	return len(parts) >= 4
}

// ExtractLocalSymbolPath extracts the file path for local symbols
// Local symbols often encode file path information in the descriptor
func ExtractLocalSymbolPath(descriptor string) string {
	// Local symbols may use file paths in their descriptors
	// This is a best-effort extraction

	// Remove trailing dots
	descriptor = strings.TrimSuffix(descriptor, ".")

	// Look for file-like paths (containing '/')
	if strings.Contains(descriptor, "/") {
		parts := strings.Split(descriptor, ".")
		for _, part := range parts {
			if strings.Contains(part, "/") {
				return part
			}
		}
	}

	return ""
}
