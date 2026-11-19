//go:build cgo

package complexity

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// Analyzer computes complexity metrics for source files.
type Analyzer struct {
	parser *Parser
}

// NewAnalyzer creates a new complexity analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		parser: NewParser(),
	}
}

// AnalyzeFile analyzes a source file and returns complexity metrics.
func (a *Analyzer) AnalyzeFile(ctx context.Context, path string) (*FileComplexity, error) {
	lang, ok := LanguageFromPath(path)
	if !ok {
		ext := strings.ToLower(filepath.Ext(path))
		msg := "unsupported file extension: " + ext
		if ext == "" {
			msg = "unsupported file: no extension and no recognised shebang"
		}
		return &FileComplexity{
			Path:  path,
			Error: msg,
		}, nil
	}

	source, err := os.ReadFile(path)
	if err != nil {
		//nolint:nilerr // error captured in Error field
		return &FileComplexity{
			Path:  path,
			Error: "failed to read file: " + err.Error(),
		}, nil
	}

	return a.AnalyzeSource(ctx, path, source, lang)
}

// AnalyzeSource analyzes source code and returns complexity metrics.
func (a *Analyzer) AnalyzeSource(ctx context.Context, path string, source []byte, lang Language) (*FileComplexity, error) {
	root, err := a.parser.Parse(ctx, source, lang)
	if err != nil {
		//nolint:nilerr // error captured in Error field
		return &FileComplexity{
			Path:     path,
			Language: lang,
			Error:    err.Error(),
		}, nil
	}

	fc := &FileComplexity{
		Path:      path,
		Language:  lang,
		Functions: make([]ComplexityResult, 0),
	}

	// Find all function nodes
	functionTypes := GetFunctionNodeTypes(lang)
	functions := FindNodes(root, functionTypes)

	for _, fn := range functions {
		result := a.analyzeFunction(fn, source, lang)
		fc.Functions = append(fc.Functions, result)
	}

	// A shell script's work is mostly its top-level commands, outside any
	// function; report them as one unit so a script without functions still
	// has a complexity.
	if lang == LangBash {
		if top, ok := analyzeTopLevel(root, source, lang, functionTypes); ok {
			fc.Functions = append(fc.Functions, top)
		}
	}

	fc.Aggregate()
	return fc, nil
}

// analyzeFunction computes complexity for a single function.
func (a *Analyzer) analyzeFunction(node *sitter.Node, source []byte, lang Language) ComplexityResult {
	name := getFunctionName(node, source, lang)
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	cyclomatic := computeCyclomaticComplexity(node, source, lang)
	cognitive := computeCognitiveComplexity(node, source, lang)

	return ComplexityResult{
		Name:       name,
		StartLine:  startLine,
		EndLine:    endLine,
		Lines:      endLine - startLine + 1,
		Cyclomatic: cyclomatic,
		Cognitive:  cognitive,
	}
}

// getFunctionName extracts the function name from a node.
func getFunctionName(node *sitter.Node, source []byte, lang Language) string {
	// Different languages have different structures for function names
	var nameNode *sitter.Node

	switch lang {
	case LangGo:
		// For Go: function_declaration has name child, method_declaration has name child
		nameNode = node.ChildByFieldName("name")
		if nameNode == nil {
			// Try to find identifier child directly
			for i := uint32(0); i < node.ChildCount(); i++ {
				child := node.Child(int(i))
				if child != nil && child.Type() == "identifier" {
					nameNode = child
					break
				}
			}
		}

	case LangJavaScript, LangTypeScript, LangTSX:
		nameNode = node.ChildByFieldName("name")
		if nameNode == nil && node.Type() == "method_definition" {
			// Method definitions have the name as first child
			nameNode = node.ChildByFieldName("name")
		}

	case LangPython, LangBash:
		nameNode = node.ChildByFieldName("name")

	case LangRust:
		nameNode = node.ChildByFieldName("name")

	case LangJava:
		nameNode = node.ChildByFieldName("name")

	case LangKotlin:
		// Kotlin function_declaration has simple_identifier as name
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

	// Anonymous function
	switch node.Type() {
	case "arrow_function", "func_literal", "lambda", "lambda_expression",
		"closure_expression", "lambda_literal", "anonymous_function":
		return "<anonymous>"
	}

	return "<unknown>"
}

// computeCyclomaticComplexity calculates cyclomatic complexity.
// Cyclomatic = E - N + 2P, but simpler: count decision points + 1
func computeCyclomaticComplexity(node *sitter.Node, source []byte, lang Language) int {
	complexity := 1 // Base complexity

	decisionTypes := GetDecisionNodeTypes(lang)
	decisionNodes := FindNodes(node, decisionTypes)

	for _, dn := range decisionNodes {
		// For binary expressions, only count if it's && or ||
		if isOperatorNodeType(dn.Type()) {
			if IsBooleanOperator(dn, source, lang) {
				complexity++
			}
		} else {
			complexity++
		}
	}

	return complexity
}

// computeCognitiveComplexity calculates cognitive complexity.
// Cognitive complexity adds weight for nesting depth.
func computeCognitiveComplexity(node *sitter.Node, source []byte, lang Language) int {
	return computeCognitiveRecursive(node, source, lang, 0, nil)
}

// TopLevelName is the name of the unit that holds a script's commands outside
// its functions (Bash only).
const TopLevelName = "<toplevel>"

// analyzeTopLevel measures the statements of root that are not inside a
// function. ok is false when there are none besides comments.
func analyzeTopLevel(root *sitter.Node, source []byte, lang Language, functionTypes []string) (ComplexityResult, bool) {
	first, last, lines := -1, -1, 0
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		if child == nil || child.Type() == "comment" || contains(functionTypes, child.Type()) {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
		lines += int(child.EndPoint().Row-child.StartPoint().Row) + 1
	}
	if first < 0 {
		return ComplexityResult{}, false
	}

	cyclomatic := 1
	for _, dn := range FindNodesSkipping(root, GetDecisionNodeTypes(lang), functionTypes) {
		if !isOperatorNodeType(dn.Type()) || IsBooleanOperator(dn, source, lang) {
			cyclomatic++
		}
	}
	cognitive := computeCognitiveRecursive(root, source, lang, 0, functionTypes)

	startLine := int(root.NamedChild(first).StartPoint().Row) + 1
	endLine := int(root.NamedChild(last).EndPoint().Row) + 1
	// StartLine-EndLine spans the top-level statements and so may enclose
	// functions; Lines counts only the statements' own lines.
	return ComplexityResult{
		Name:       TopLevelName,
		StartLine:  startLine,
		EndLine:    endLine,
		Lines:      lines,
		Cyclomatic: cyclomatic,
		Cognitive:  cognitive,
	}, true
}

// computeCognitiveRecursive sums cognitive complexity below node. Children
// whose type is in skip are not visited.
func computeCognitiveRecursive(node *sitter.Node, source []byte, lang Language, nestingLevel int, skip []string) int {
	complexity := 0

	decisionTypes := GetDecisionNodeTypes(lang)
	nestingTypes := GetNestingNodeTypes(lang)

	nodeType := node.Type()

	// Check if this node adds to complexity
	isDecision := contains(decisionTypes, nodeType)
	isNesting := contains(nestingTypes, nodeType)

	if isDecision {
		// For binary expressions, only count if it's && or ||
		if isOperatorNodeType(nodeType) {
			if IsBooleanOperator(node, source, lang) {
				complexity += 1 + nestingLevel
			}
		} else {
			// Add 1 for the construct plus nesting penalty
			complexity += 1 + nestingLevel
		}
	}

	// Determine nesting level for children
	childNesting := nestingLevel
	if isNesting {
		childNesting++
	}

	// Recurse into children
	for i := uint32(0); i < node.ChildCount(); i++ {
		child := node.Child(int(i))
		if child != nil && !contains(skip, child.Type()) {
			complexity += computeCognitiveRecursive(child, source, lang, childNesting, skip)
		}
	}

	return complexity
}

// FindNodes finds all nodes of the given types in the AST.
func FindNodes(root *sitter.Node, types []string) []*sitter.Node {
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

// FindNodesSkipping finds all nodes of the given types but does not recurse
// into nodes whose type is in skipTypes. This is useful for scope-aware
// searches, e.g. finding defer_statements inside a for loop without
// descending into nested func_literals (closures).
func FindNodesSkipping(root *sitter.Node, types []string, skipTypes []string) []*sitter.Node {
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
			child := node.Child(int(i))
			if child != nil && contains(skipTypes, child.Type()) {
				continue
			}
			walk(child)
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

// IsAvailable returns whether complexity analysis is available.
// Returns true when CGO is enabled.
func IsAvailable() bool {
	return true
}
