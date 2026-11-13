package diff

import (
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// withTypeBodyExtents gives each braced type the index knows only by the line of its
// name the lines its body spans: a Go struct or interface, a TypeScript interface,
// class, enum or object type, a Rust struct, enum, union or trait. scip-go gives types
// no EnclosingRange, and neither do older scip-typescript and rust-analyzer builds, so
// a field or variant inserted into the body belongs to no definition and the change
// comes back as a file-level entry. lang is declarationLanguage's, lines the index's
// side of the file. The body is read with the done check's readers (typeBody,
// bracedBodyEnd), and only where the name's line declares that type and every line up
// to the closing brace is shown. The definitions read are copies: the document may be
// shared.
func withTypeBodyExtents(defs []*SymbolDefInfo, lines map[int]string, lang string) []*SymbolDefInfo {
	if len(lines) == 0 || (lang != "go" && lang != "ts" && lang != "rust") {
		return defs
	}
	out, copied := defs, false
	for i, def := range defs {
		if def.Precision != scip.BoundsPoint || !isTypeSymbol(def.Symbol) {
			continue
		}
		line := def.DefLine
		if line == 0 {
			line = def.StartLine
		}
		name := scip.SymbolSimpleName(def.Symbol)
		if name == "" {
			name = def.Name
		}
		end := typeBodyEnd(lines, line, lang, name)
		if end <= def.EndLine {
			continue
		}
		if !copied {
			out, copied = append([]*SymbolDefInfo(nil), defs...), true
		}
		extended := *def
		extended.EndLine = end
		out[i] = &extended
	}
	return out
}

// isTypeSymbol says whether a SCIP symbol names a type: its last descriptor is `Name#`.
func isTypeSymbol(symbol string) bool {
	return strings.HasSuffix(symbol, "#") && !scip.IsLocalSymbol(symbol)
}

// isTextExtendedType says whether a definition is a type whose body was read from the
// text: the index gave it only the line of its name.
func isTextExtendedType(def *SymbolDefInfo) bool {
	return def.Precision == scip.BoundsPoint && isTypeSymbol(def.Symbol)
}

// typeBodyEnd returns the 1-based line of the brace closing the body of the type
// declared on line, or 0 if that line declares no braced type of that name or the body
// runs past what the side shows.
func typeBodyEnd(lines map[int]string, line int, lang, name string) int {
	if name == "" {
		return 0
	}
	first, ok := lines[line]
	if !ok {
		return 0
	}
	re := typeBodyDeclRe(lang, name)
	m := re.FindStringIndex(first)
	if m == nil {
		return 0
	}
	// The contiguous run of shown lines from the declaration on.
	var run []string
	for l := line; l < line+maxRustBodyLines; l++ {
		text, shown := lines[l]
		if !shown {
			break
		}
		run = append(run, text)
	}
	if lang == "rust" {
		_, end, state := bracedBodyEnd(run, 0, m[1])
		if state != BodyRead {
			return 0
		}
		return line + end
	}
	_, end, ok := typeBody(run, 0, "")
	if !ok {
		return 0
	}
	return line + end
}

// typeBodyDeclRe matches a line declaring the named type with a braced body, as the
// field readers (bracedTypeRe) do, and Go interfaces, TypeScript enums and Rust unions
// and traits besides. For Rust the match ends past the name, where bracedBodyEnd starts.
func typeBodyDeclRe(lang, name string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(name)
	switch lang {
	case "go":
		return regexp.MustCompile(`^\s*(?:type\s+)?` + quoted + `(?:\[[^\]]*\])?\s+(?:struct|interface)\s*\{`)
	case "ts":
		return regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:(?:interface|class)\s+` + quoted + `\b|(?:const\s+)?enum\s+` + quoted + `\b|type\s+` + quoted + `(?:<[^=]*>)?\s*=\s*\{)`)
	default:
		return regexp.MustCompile(`^\s*(?:pub(?:\s*\([^)]*\))?\s+)?(?:unsafe\s+)?(?:struct|enum|union|trait)\s+` + quoted + `\b`)
	}
}
