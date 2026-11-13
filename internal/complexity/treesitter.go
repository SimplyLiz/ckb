//go:build cgo

package complexity

import (
	"context"
	"fmt"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// Parser wraps tree-sitter for multi-language parsing.
// The underlying sitter.Parser is not thread-safe; mu serializes concurrent calls.
type Parser struct {
	mu     sync.Mutex
	parser *sitter.Parser
}

// NewParser creates a new tree-sitter parser.
func NewParser() *Parser {
	return &Parser{
		parser: sitter.NewParser(),
	}
}

// Parse parses source code and returns the AST root node.
func (p *Parser) Parse(ctx context.Context, source []byte, lang Language) (*sitter.Node, error) {
	tsLang, err := getLanguage(lang)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.parser.SetLanguage(tsLang)
	tree, err := p.parser.ParseCtx(ctx, nil, source)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	return tree.RootNode(), nil
}

// getLanguage returns the tree-sitter Language for a given language identifier.
func getLanguage(lang Language) (*sitter.Language, error) {
	switch lang {
	case LangGo:
		return golang.GetLanguage(), nil
	case LangJavaScript:
		return javascript.GetLanguage(), nil
	case LangTypeScript:
		return typescript.GetLanguage(), nil
	case LangTSX:
		return tsx.GetLanguage(), nil
	case LangPython:
		return python.GetLanguage(), nil
	case LangRust:
		return rust.GetLanguage(), nil
	case LangJava:
		return java.GetLanguage(), nil
	case LangKotlin:
		return kotlin.GetLanguage(), nil
	case LangBash:
		return bash.GetLanguage(), nil
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
}

// GetFunctionNodeTypes returns the node types that represent functions for a language.
func GetFunctionNodeTypes(lang Language) []string {
	switch lang {
	case LangGo:
		return []string{"function_declaration", "method_declaration", "func_literal"}
	case LangJavaScript, LangTypeScript, LangTSX:
		return []string{"function_declaration", "function_expression", "arrow_function", "method_definition", "generator_function_declaration"}
	case LangPython:
		return []string{"function_definition", "lambda"}
	case LangRust:
		return []string{"function_item", "closure_expression"}
	case LangJava:
		return []string{"method_declaration", "constructor_declaration", "lambda_expression"}
	case LangKotlin:
		return []string{"function_declaration", "lambda_literal", "anonymous_function"}
	case LangBash:
		// Both "foo() { ... }" and "function foo { ... }".
		return []string{"function_definition"}
	default:
		return nil
	}
}

// GetDecisionNodeTypes returns the node types that contribute to cyclomatic complexity.
func GetDecisionNodeTypes(lang Language) []string {
	switch lang {
	case LangGo:
		return []string{
			"if_statement",
			"for_statement",
			"range_clause",
			"expression_case",    // case in switch
			"type_case",          // case in type switch
			"select_statement",   // select with cases
			"communication_case", // case in select
			"binary_expression",  // for && and ||
		}
	case LangJavaScript, LangTypeScript, LangTSX:
		return []string{
			"if_statement",
			"for_statement",
			"for_in_statement",
			"while_statement",
			"do_statement",
			"switch_case",
			"catch_clause",
			"ternary_expression",
			"binary_expression", // for && and ||
			"optional_chain_expression",
		}
	case LangPython:
		return []string{
			"if_statement",
			"elif_clause",
			"for_statement",
			"while_statement",
			"except_clause",
			"with_statement",
			"boolean_operator",         // and, or
			"conditional_expression",   // ternary
			"list_comprehension",       // for clause
			"dictionary_comprehension", // for clause
			"set_comprehension",        // for clause
			"generator_expression",     // for clause
		}
	case LangRust:
		return []string{
			"if_expression",
			"match_expression",
			"match_arm",
			"while_expression",
			"loop_expression",
			"for_expression",
			"binary_expression", // for && and ||
		}
	case LangJava:
		return []string{
			"if_statement",
			"for_statement",
			"enhanced_for_statement",
			"while_statement",
			"do_statement",
			"switch_expression",
			"switch_block_statement_group",
			"catch_clause",
			"ternary_expression",
			"binary_expression", // for && and ||
		}
	case LangKotlin:
		return []string{
			"if_expression",
			"when_expression",
			"when_entry",
			"for_statement",
			"while_statement",
			"do_while_statement",
			"catch_block",
			"binary_expression", // for && and ||
			"elvis_expression",  // ?:
		}
	case LangBash:
		return []string{
			"if_statement",
			"elif_clause",
			"case_item", // each arm of a case, like a switch case
			"for_statement",
			"c_style_for_statement",
			"while_statement",   // also "until": the grammar uses one node
			"list",              // cmd1 && cmd2, cmd1 || cmd2
			"binary_expression", // && and || inside [[ ]] and (( ))
			"ternary_expression",
		}
	default:
		return nil
	}
}

// IsBooleanOperator checks if a binary expression node is && or ||.
func IsBooleanOperator(node *sitter.Node, source []byte, lang Language) bool {
	if !isOperatorNodeType(node.Type()) {
		return false
	}

	// Find the operator child
	for i := uint32(0); i < node.ChildCount(); i++ {
		child := node.Child(int(i))
		if child == nil {
			continue
		}

		switch lang {
		case LangGo, LangJavaScript, LangTypeScript, LangTSX, LangRust, LangJava, LangKotlin, LangBash:
			content := string(source[child.StartByte():child.EndByte()])
			if content == "&&" || content == "||" {
				return true
			}
		case LangPython:
			// Python uses 'and' and 'or' keywords
			if child.Type() == "and" || child.Type() == "or" {
				return true
			}
		}
	}

	return false
}

// isOperatorNodeType reports whether nodes of this type count toward
// complexity only when their operator is a boolean one (&&, ||, and, or).
// Bash's "list" is a command list joined by && or ||.
func isOperatorNodeType(t string) bool {
	return t == "binary_expression" || t == "boolean_operator" || t == "list"
}

// GetNestingNodeTypes returns node types that increase nesting depth for cognitive complexity.
func GetNestingNodeTypes(lang Language) []string {
	switch lang {
	case LangGo:
		return []string{
			"if_statement",
			"for_statement",
			"select_statement",
			"type_switch_statement",
			"expression_switch_statement",
			"func_literal", // nested functions
		}
	case LangJavaScript, LangTypeScript, LangTSX:
		return []string{
			"if_statement",
			"for_statement",
			"for_in_statement",
			"while_statement",
			"do_statement",
			"switch_statement",
			"try_statement",
			"arrow_function",
			"function_expression",
		}
	case LangPython:
		return []string{
			"if_statement",
			"for_statement",
			"while_statement",
			"try_statement",
			"with_statement",
			"lambda",
			"list_comprehension",
			"dictionary_comprehension",
			"set_comprehension",
			"generator_expression",
		}
	case LangRust:
		return []string{
			"if_expression",
			"match_expression",
			"while_expression",
			"loop_expression",
			"for_expression",
			"closure_expression",
		}
	case LangJava:
		return []string{
			"if_statement",
			"for_statement",
			"enhanced_for_statement",
			"while_statement",
			"do_statement",
			"switch_expression",
			"try_statement",
			"lambda_expression",
		}
	case LangKotlin:
		return []string{
			"if_expression",
			"when_expression",
			"for_statement",
			"while_statement",
			"do_while_statement",
			"try_expression",
			"lambda_literal",
		}
	case LangBash:
		return []string{
			"if_statement",
			"case_statement",
			"for_statement",
			"c_style_for_statement",
			"while_statement",
			"subshell",
		}
	default:
		return nil
	}
}
