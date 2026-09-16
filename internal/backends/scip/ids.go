package scip

import (
	"fmt"
	"strings"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"
)

// SCIPIdentifier represents a parsed SCIP symbol identifier
// SCIP format: <scheme> <manager> <package> <version> <descriptor>
// Example: scip-typescript npm @types/node 18.0.0 process.
type SCIPIdentifier struct {
	// Scheme is the indexer scheme (e.g., "scip-typescript", "scip-go")
	Scheme string

	// Manager is the package manager (e.g., "npm", "go", "maven")
	Manager string

	// Package is the package name
	Package string

	// Version is the package version. May be empty (some legacy/local
	// identifiers don't carry one).
	Version string

	// Descriptor is the symbol descriptor path, reconstructed in canonical
	// SCIP form (e.g. "Handler#handle()."). Kept for callers that still
	// want the raw descriptor text (GetSymbolSignature, IsMethodDescriptor).
	Descriptor string

	// Raw is the original SCIP identifier
	Raw string

	// descriptors holds the parsed, suffix-aware descriptor chain from the
	// official scip bindings parser. nil when the identifier couldn't be
	// parsed by the strict SCIP grammar (e.g. malformed/legacy test
	// fixtures, or "local ..." symbols) — in that case the exported
	// methods below fall back to the legacy string-heuristic
	// implementations operating on Descriptor.
	descriptors []*bindingscip.Descriptor
}

// ParseSCIPIdentifier parses a SCIP symbol identifier.
//
// This delegates to the official scip bindings parser
// (github.com/sourcegraph/scip/bindings/go/scip), which is
// descriptor-aware: it splits the identifier into scheme, package
// (manager/name/version — each of which may be a "." placeholder, and may
// contain spaces escaped by doubling per the SCIP spec), and a chain of
// descriptors with their suffix kind (namespace '/', type '#', term '.',
// method "().", type-parameter '[..]', parameter '(..)', meta ':', macro
// '!'), correctly unescaping backtick-quoted names (e.g. "`<constructor>`",
// or a name containing a literal backtick escaped as "``").
//
// Identifiers that don't conform to the strict SCIP grammar (e.g. "local
// ..." symbols, or malformed/legacy test fixtures using a 4-field form
// with no package version) fall back to a lenient legacy parser so callers
// that construct ad-hoc identifiers keep working; in that case the derived
// methods below (GetSimpleName, GetContainerName, ExtractSymbolKind, ...)
// fall back to string-heuristics over the reconstructed Descriptor field.
//
// Examples:
//
//	scip-typescript npm @types/node 18.0.0 process.
//	scip-go gomod ckb a6af7cfb2eff `ckb/internal/api`/NewServer().
//	scip-java maven com.google.guava  guava 31.0 ImmutableList#
func ParseSCIPIdentifier(id string) (*SCIPIdentifier, error) {
	if id == "" {
		return nil, fmt.Errorf("empty SCIP identifier")
	}

	if sym, err := bindingscip.ParseSymbol(id); err == nil && sym.Package != nil {
		result := &SCIPIdentifier{
			Scheme:      sym.Scheme,
			Manager:     sym.Package.Manager,
			Package:     sym.Package.Name,
			Version:     sym.Package.Version,
			Raw:         id,
			descriptors: sym.Descriptors,
		}
		result.Descriptor = formatDescriptors(sym.Descriptors)
		return result, nil
	}

	return parseLegacySCIPIdentifier(id)
}

// parseLegacySCIPIdentifier is the original hand-rolled splitter, kept as a
// fallback for identifiers the strict SCIP grammar parser rejects (e.g.
// synthetic test fixtures missing the package-version field, or "local ..."
// symbols with no package/descriptor at all).
func parseLegacySCIPIdentifier(id string) (*SCIPIdentifier, error) {
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
		result.Version = parts[3]
		result.Descriptor = parts[4]
	}

	return result, nil
}

// formatDescriptors reconstructs the canonical SCIP descriptor string (with
// suffix punctuation) from a parsed descriptor chain, e.g.
// [{Handler Type} {handle Method}] -> "Handler#handle().".
func formatDescriptors(descriptors []*bindingscip.Descriptor) string {
	formatter := bindingscip.DescriptorOnlyFormatter
	return formatter.FormatDescriptors(descriptors)
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

// GetSimpleName extracts the short display name: the name of the last
// descriptor in the chain (e.g. "handle" for "Handler#handle().", not
// "Handler#handle" or "Handler.handle").
//
// Examples:
//   - "process.env.NODE_ENV." -> "NODE_ENV"
//   - "`ckb/internal/api`/NewServer()." -> "NewServer"
//   - "`ckb/internal/api`/Server#" -> "Server"
//   - "`ckb/internal/api`/Server#Method()." -> "Method"
func (s *SCIPIdentifier) GetSimpleName() string {
	if s.descriptors != nil {
		if len(s.descriptors) == 0 {
			return ""
		}
		return s.descriptors[len(s.descriptors)-1].Name
	}
	return s.legacyGetSimpleName()
}

// legacyGetSimpleName is the original heuristic implementation, used as a
// fallback when the identifier didn't parse through the strict SCIP
// grammar (s.descriptors == nil).
func (s *SCIPIdentifier) legacyGetSimpleName() string {
	descriptor := s.Descriptor

	// Remove trailing '.' or '#'
	descriptor = strings.TrimSuffix(descriptor, ".")
	descriptor = strings.TrimSuffix(descriptor, "#")

	// Handle scip-go format with backtick-quoted package paths
	// Format: `package/path`/Symbol() or `package/path`/Type or
	// `package/path`/Type#Method() (a method on a type)
	if strings.Contains(descriptor, "`") {
		lastBacktick := strings.LastIndex(descriptor, "`")
		if lastBacktick != -1 && lastBacktick < len(descriptor)-1 {
			remainder := descriptor[lastBacktick+1:]
			if idx := strings.LastIndex(remainder, "/"); idx != -1 {
				name := remainder[idx+1:]
				name = strings.TrimSuffix(name, "()")
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

// GetContainerName extracts the name of the nearest enclosing type
// descriptor (suffix '#'), e.g. "Handler" for both "Handler#handle()."
// and "Handler#handle().(input)". Returns "" when the symbol has no
// enclosing type (e.g. a package-level function or namespace member).
func (s *SCIPIdentifier) GetContainerName() string {
	if s.descriptors != nil {
		for i := len(s.descriptors) - 2; i >= 0; i-- {
			if s.descriptors[i].Suffix == bindingscip.Descriptor_Type {
				return s.descriptors[i].Name
			}
		}
		return ""
	}
	return s.legacyGetContainerName()
}

// legacyGetContainerName is the original heuristic implementation.
func (s *SCIPIdentifier) legacyGetContainerName() string {
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

// ExtractSymbolKind infers the symbol kind from the last descriptor's
// suffix:
//   - Method: "method" if the parent descriptor is a type, else "function"
//   - Parameter / TypeParameter: "parameter"
//   - Type: "class"
//   - Namespace: "namespace"
//   - Local: "variable"
//   - Term (field/property/constant): "constant" if the name is all
//     uppercase, else "property"
func (s *SCIPIdentifier) ExtractSymbolKind() SymbolKind {
	if s.descriptors != nil {
		return kindFromDescriptors(s.descriptors)
	}
	return s.legacyExtractSymbolKind()
}

func kindFromDescriptors(descriptors []*bindingscip.Descriptor) SymbolKind {
	if len(descriptors) == 0 {
		return KindUnknown
	}

	last := descriptors[len(descriptors)-1]
	switch last.Suffix {
	case bindingscip.Descriptor_Method:
		if len(descriptors) >= 2 && descriptors[len(descriptors)-2].Suffix == bindingscip.Descriptor_Type {
			return KindMethod
		}
		return KindFunction
	case bindingscip.Descriptor_Parameter, bindingscip.Descriptor_TypeParameter:
		return KindParameter
	case bindingscip.Descriptor_Type:
		return KindClass
	case bindingscip.Descriptor_Namespace:
		return KindNamespace
	case bindingscip.Descriptor_Local:
		return KindVariable
	case bindingscip.Descriptor_Macro:
		return KindFunction
	case bindingscip.Descriptor_Meta:
		return KindType
	default: // Descriptor_Term
		if last.Name == strings.ToUpper(last.Name) && len(last.Name) > 1 {
			return KindConstant
		}
		return KindProperty
	}
}

// legacyExtractSymbolKind is the original heuristic implementation.
func (s *SCIPIdentifier) legacyExtractSymbolKind() SymbolKind {
	descriptor := s.Descriptor

	if descriptor == "" {
		return KindUnknown
	}

	hasParens := strings.Contains(descriptor, "(")
	hasReceiver := strings.Contains(descriptor, "#")

	if hasParens && hasReceiver {
		return KindMethod
	}

	if hasParens {
		return KindFunction
	}

	if hasReceiver {
		return KindClass
	}

	simpleName := s.GetSimpleName()
	if simpleName == strings.ToUpper(simpleName) && len(simpleName) > 1 {
		return KindConstant
	}

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

// PackageInfo holds package coordinate information for a SCIP identifier.
type PackageInfo struct {
	Manager string
	Name    string
	Version string
}

// GetPackageInfo extracts package information from the SCIP identifier.
// Manager/Package/Version are already parsed per-field by ParseSCIPIdentifier
// (via the official bindings, or the legacy fallback), so this just wraps
// them — it no longer re-splits s.Package on whitespace, which used to
// mis-parse multi-word package coordinates (e.g. Java's "groupId artifactId").
func (s *SCIPIdentifier) GetPackageInfo() *PackageInfo {
	return &PackageInfo{
		Manager: s.Manager,
		Name:    s.Package,
		Version: s.Version,
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
