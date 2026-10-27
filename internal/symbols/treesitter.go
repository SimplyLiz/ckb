//go:build cgo

// Package symbols provides tree-sitter based symbol extraction for code intelligence fallback.
package symbols

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/SimplyLiz/CodeMCP/internal/complexity"
)

// Symbol represents an extracted symbol from source code.
type Symbol struct {
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`       // "function", "method", "class", "type", "interface"
	Path       string  `json:"path"`       // File path
	Line       int     `json:"line"`       // Start line (1-indexed)
	EndLine    int     `json:"endLine"`    // End line (1-indexed)
	Container  string  `json:"container"`  // Parent class/struct/impl name for methods
	Signature  string  `json:"signature"`  // Full signature from source
	Source     string  `json:"source"`     // "treesitter"
	Confidence float64 `json:"confidence"` // 0.7 for tree-sitter
}

// Extractor extracts symbols from source files using tree-sitter.
type Extractor struct {
	parser *complexity.Parser
}

// NewExtractor creates a new symbol extractor.
func NewExtractor() *Extractor {
	return &Extractor{
		parser: complexity.NewParser(),
	}
}

// ExtractFile extracts all symbols from a single file.
func (e *Extractor) ExtractFile(ctx context.Context, path string) ([]Symbol, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(path))
	lang, ok := complexity.LanguageFromExtension(ext)
	if !ok {
		return nil, nil // Unsupported language, return empty
	}

	return e.ExtractSource(ctx, path, source, lang)
}

// ExtractSource extracts symbols from source bytes.
func (e *Extractor) ExtractSource(ctx context.Context, path string, source []byte, lang complexity.Language) ([]Symbol, error) {
	root, err := e.parser.Parse(ctx, source, lang)
	if err != nil {
		return nil, err
	}

	var symbols []Symbol

	// Extract functions
	functionTypes := getFunctionNodeTypes(lang)
	functions := findNodes(root, functionTypes)
	for _, fn := range functions {
		sym := e.extractFunction(fn, source, lang, path, "")
		if sym != nil {
			symbols = append(symbols, *sym)
		}
	}

	// Extract classes/types/interfaces
	classTypes := getClassNodeTypes(lang)
	classes := findNodes(root, classTypes)
	for _, cls := range classes {
		sym := e.extractClass(cls, source, lang, path)
		if sym != nil {
			symbols = append(symbols, *sym)
			// Extract methods inside the class
			methods := e.extractMethods(cls, source, lang, path, sym.Name)
			symbols = append(symbols, methods...)
		}
	}

	return symbols, nil
}

// ExtractDirectory extracts all symbols under root. See
// ExtractDirectoryWithOptions for which files are visited.
func (e *Extractor) ExtractDirectory(ctx context.Context, root string, filter func(string) bool) ([]Symbol, error) {
	return e.ExtractDirectoryWithOptions(ctx, root, DirOptions{Filter: filter})
}

// ExtractDirectoryWithOptions extracts all symbols under root.
//
// When root is inside a git work tree the file list comes from
// `git ls-files -co --exclude-standard`, so .gitignore (including nested ones)
// is honoured. Otherwise, or if git fails, the directory is walked. In both
// cases dot-directories, node_modules, vendor and __pycache__ are skipped and
// opts.Exclude globs are applied.
func (e *Extractor) ExtractDirectoryWithOptions(ctx context.Context, root string, opts DirOptions) ([]Symbol, error) {
	files, err := listSourceFiles(ctx, root, opts)
	if err != nil {
		return nil, err
	}

	var allSymbols []Symbol
	for _, p := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if opts.Filter != nil && !opts.Filter(p) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p))
		if _, ok := complexity.LanguageFromExtension(ext); !ok {
			continue
		}
		symbols, extractErr := e.ExtractFile(ctx, p)
		if extractErr != nil {
			continue // Intentionally skip files with parse errors
		}
		allSymbols = append(allSymbols, symbols...)
	}
	return allSymbols, nil
}

// extractFunction extracts a symbol from a function node.
func (e *Extractor) extractFunction(node *sitter.Node, source []byte, lang complexity.Language, path, container string) *Symbol {
	name := getFunctionName(node, source, lang)
	if name == "" || name == "<unknown>" {
		return nil
	}

	kind := "function"
	if node.Type() == "method_declaration" || node.Type() == "method_definition" {
		kind = "method"
	}
	// Detect if it's a method based on container
	if container != "" {
		kind = "method"
	}

	return &Symbol{
		Name:       name,
		Kind:       kind,
		Path:       path,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Container:  container,
		Signature:  extractSignature(node, source, lang),
		Source:     "treesitter",
		Confidence: 0.7,
	}
}

// extractClass extracts a symbol from a class/type node.
func (e *Extractor) extractClass(node *sitter.Node, source []byte, lang complexity.Language, path string) *Symbol {
	name := getClassName(node, source, lang)
	if name == "" {
		return nil
	}

	kind := getClassKind(node, lang)

	return &Symbol{
		Name:       name,
		Kind:       kind,
		Path:       path,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Container:  "",
		Signature:  extractClassSignature(node, source, lang),
		Source:     "treesitter",
		Confidence: 0.7,
	}
}

// extractMethods extracts method symbols from inside a class/type.
func (e *Extractor) extractMethods(classNode *sitter.Node, source []byte, lang complexity.Language, path, className string) []Symbol {
	var methods []Symbol

	methodTypes := getMethodNodeTypes(lang)
	methodNodes := findNodes(classNode, methodTypes)

	for _, m := range methodNodes {
		sym := e.extractFunction(m, source, lang, path, className)
		if sym != nil {
			methods = append(methods, *sym)
		}
	}

	return methods
}

// getFunctionNodeTypes returns node types for functions (not methods inside classes).
func getFunctionNodeTypes(lang complexity.Language) []string {
	switch lang {
	case complexity.LangGo:
		return []string{"function_declaration", "method_declaration"}
	case complexity.LangJavaScript, complexity.LangTypeScript, complexity.LangTSX:
		return []string{"function_declaration", "arrow_function", "generator_function_declaration"}
	case complexity.LangPython:
		return []string{"function_definition"}
	case complexity.LangRust:
		return []string{"function_item"}
	case complexity.LangJava:
		// Top-level methods are inside class bodies, handled separately
		return []string{}
	case complexity.LangKotlin:
		return []string{"function_declaration"}
	default:
		return nil
	}
}

// getClassNodeTypes returns node types for classes/types/interfaces.
func getClassNodeTypes(lang complexity.Language) []string {
	switch lang {
	case complexity.LangGo:
		return []string{"type_declaration"}
	case complexity.LangJavaScript, complexity.LangTypeScript, complexity.LangTSX:
		return []string{"class_declaration", "interface_declaration"}
	case complexity.LangPython:
		return []string{"class_definition"}
	case complexity.LangRust:
		return []string{"struct_item", "enum_item", "trait_item", "impl_item"}
	case complexity.LangJava:
		return []string{"class_declaration", "interface_declaration", "enum_declaration"}
	case complexity.LangKotlin:
		return []string{"class_declaration", "interface_declaration", "object_declaration"}
	default:
		return nil
	}
}

// getMethodNodeTypes returns node types for methods inside classes.
func getMethodNodeTypes(lang complexity.Language) []string {
	switch lang {
	case complexity.LangGo:
		return nil // Go methods are at top level with receivers
	case complexity.LangJavaScript, complexity.LangTypeScript, complexity.LangTSX:
		return []string{"method_definition"}
	case complexity.LangPython:
		return []string{"function_definition"}
	case complexity.LangRust:
		return []string{"function_item"} // Inside impl blocks
	case complexity.LangJava:
		return []string{"method_declaration", "constructor_declaration"}
	case complexity.LangKotlin:
		return []string{"function_declaration"}
	default:
		return nil
	}
}

// getFunctionName extracts the function name from a node.
func getFunctionName(node *sitter.Node, source []byte, lang complexity.Language) string {
	var nameNode *sitter.Node

	switch lang {
	case complexity.LangGo:
		nameNode = node.ChildByFieldName("name")
		if nameNode == nil {
			for i := uint32(0); i < node.ChildCount(); i++ {
				child := node.Child(int(i))
				if child != nil && child.Type() == "identifier" {
					nameNode = child
					break
				}
			}
		}

	case complexity.LangJavaScript, complexity.LangTypeScript, complexity.LangTSX:
		nameNode = node.ChildByFieldName("name")

	case complexity.LangPython:
		nameNode = node.ChildByFieldName("name")

	case complexity.LangRust:
		nameNode = node.ChildByFieldName("name")

	case complexity.LangJava:
		nameNode = node.ChildByFieldName("name")

	case complexity.LangKotlin:
		for i := uint32(0); i < node.ChildCount(); i++ {
			child := node.Child(int(i))
			if child != nil && child.Type() == "simple_identifier" {
				nameNode = child
				break
			}
		}
	}

	if nameNode != nil {
		return string(source[nameNode.StartByte():nameNode.EndByte()])
	}

	// Check for anonymous functions
	switch node.Type() {
	case "arrow_function", "func_literal", "lambda", "lambda_expression",
		"closure_expression", "lambda_literal", "anonymous_function":
		return "<anonymous>"
	}

	return ""
}

// getClassName extracts the class/type name from a node.
func getClassName(node *sitter.Node, source []byte, lang complexity.Language) string {
	var nameNode *sitter.Node

	switch lang {
	case complexity.LangGo:
		// type_declaration has type_spec child which has the name
		for i := uint32(0); i < node.ChildCount(); i++ {
			child := node.Child(int(i))
			if child != nil && child.Type() == "type_spec" {
				nameNode = child.ChildByFieldName("name")
				break
			}
		}

	case complexity.LangJavaScript, complexity.LangTypeScript, complexity.LangTSX:
		nameNode = node.ChildByFieldName("name")

	case complexity.LangPython:
		nameNode = node.ChildByFieldName("name")

	case complexity.LangRust:
		nameNode = node.ChildByFieldName("name")
		// For impl blocks, try to get the type being implemented
		if nameNode == nil && node.Type() == "impl_item" {
			// impl_item has type child
			for i := uint32(0); i < node.ChildCount(); i++ {
				child := node.Child(int(i))
				if child != nil && child.Type() == "type_identifier" {
					nameNode = child
					break
				}
			}
		}

	case complexity.LangJava, complexity.LangKotlin:
		nameNode = node.ChildByFieldName("name")
		if nameNode == nil {
			// Try identifier
			for i := uint32(0); i < node.ChildCount(); i++ {
				child := node.Child(int(i))
				if child != nil && (child.Type() == "identifier" || child.Type() == "simple_identifier") {
					nameNode = child
					break
				}
			}
		}
	}

	if nameNode != nil {
		return string(source[nameNode.StartByte():nameNode.EndByte()])
	}

	return ""
}

// getClassKind determines the kind of class/type node.
func getClassKind(node *sitter.Node, lang complexity.Language) string {
	nodeType := node.Type()

	switch lang {
	case complexity.LangGo:
		return "type" // Go has type declarations (struct, interface, etc.)

	case complexity.LangJavaScript, complexity.LangTypeScript, complexity.LangTSX:
		if nodeType == "interface_declaration" {
			return "interface"
		}
		return "class"

	case complexity.LangPython:
		return "class"

	case complexity.LangRust:
		switch nodeType {
		case "struct_item":
			return "type"
		case "enum_item":
			return "type"
		case "trait_item":
			return "interface"
		case "impl_item":
			return "type" // impl blocks extend types
		}
		return "type"

	case complexity.LangJava, complexity.LangKotlin:
		switch nodeType {
		case "interface_declaration":
			return "interface"
		case "enum_declaration":
			return "type"
		case "object_declaration": // Kotlin object
			return "class"
		}
		return "class"
	}

	return "type"
}

// extractSignature extracts a function signature from source.
func extractSignature(node *sitter.Node, source []byte, lang complexity.Language) string {
	// Get the first line as signature (simplified)
	startByte := node.StartByte()
	endByte := node.EndByte()

	// Find the end of the first line or opening brace
	text := source[startByte:endByte]
	for i, b := range text {
		if b == '\n' || b == '{' {
			return strings.TrimSpace(string(text[:i]))
		}
	}

	// Short function, return all
	if len(text) < 200 {
		return strings.TrimSpace(string(text))
	}
	return strings.TrimSpace(string(text[:200])) + "..."
}

// extractClassSignature extracts a class/type signature from source.
func extractClassSignature(node *sitter.Node, source []byte, lang complexity.Language) string {
	// Get the first line as signature
	startByte := node.StartByte()
	endByte := node.EndByte()

	text := source[startByte:endByte]
	for i, b := range text {
		if b == '\n' || b == '{' || b == ':' {
			sig := strings.TrimSpace(string(text[:i]))
			if sig != "" {
				return sig
			}
		}
	}

	if len(text) < 100 {
		return strings.TrimSpace(string(text))
	}
	return strings.TrimSpace(string(text[:100])) + "..."
}

// findNodes finds all nodes of the given types in the AST.
func findNodes(root *sitter.Node, types []string) []*sitter.Node {
	if len(types) == 0 {
		return nil
	}

	var result []*sitter.Node

	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil {
			return
		}

		if contains(types, node.Type()) {
			result = append(result, node)
		}

		for i := uint32(0); i < node.ChildCount(); i++ {
			walk(node.Child(int(i)))
		}
	}

	walk(root)
	return result
}

// contains checks if a slice contains a string.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// IsAvailable returns whether symbol extraction is available.
func IsAvailable() bool {
	return complexity.IsAvailable()
}
