package scip

import (
	"strings"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"
)

// scip_dart gives no occurrence an EnclosingRange, so where a definition ends
// has to be inferred. Closing a function off at the next function, as for
// other indexers, puts everything between two functions into the first one:
// `final syncRoundTimeoutProvider = Provider((ref) => SyncService.roundTimeout)`
// below a method counted as that method calling SyncService, and the
// annotation `@ProviderFor(SyncService)` above a top-level variable, before any
// function, belonged to nothing. Riverpod's generated providers are exactly
// that shape, so every use of a Notifier through its provider was invisible to
// impact analysis.
//
// scip_dart writes a document's occurrences in syntax-tree order: a
// declaration's defining name first, then its annotations, return type and
// body, then the next declaration (`@override` on line 87 follows the
// definition on line 88). orderedBounds uses that order, not lines, to say
// which definition an occurrence belongs to.

// usesOrderedBounds reports whether doc is a Dart document without any
// EnclosingRange, the case orderedBounds is written for.
func usesOrderedBounds(doc *Document) bool {
	if !strings.HasSuffix(doc.RelativePath, ".dart") {
		return false
	}
	for _, occ := range doc.Occurrences {
		if len(occ.EnclosingRange) > 0 {
			return false
		}
	}
	return true
}

// orderedBounds returns the bounds of doc's functions and of its top-level
// variables (holders: what a variable's initializer references, the variable
// depends on). Each starts at its first occurrence, annotations included, and
// ends before the next declaration that isn't nested in it.
func orderedBounds(doc *Document) (funcs, holders []DefinitionBound) {
	kinds := make(map[string]int32, len(doc.Symbols))
	for _, sym := range doc.Symbols {
		kinds[sym.Symbol] = sym.Kind
	}

	type item struct {
		symbol           string
		defLine          int
		lines            []int // of the occurrences it holds, in document order
		start, last      int
		callable, holder bool
	}
	var items []*item
	seen := make(map[string]bool)
	// Occurrences before the first declaration (a part file's leading
	// annotations) wait in a pseudo-item that no bound comes from.
	cur := &item{}
	for _, occ := range doc.Occurrences {
		if len(occ.Range) == 0 {
			continue
		}
		line := int(occ.Range[0]) // #nosec G115 -- SCIP int32 fits in int
		if occ.SymbolRoles&SymbolRoleDefinition != 0 && occ.Symbol != "" && !seen[occ.Symbol] &&
			!IsLocalSymbol(occ.Symbol) && !isContainerOrParameterKind(kinds[occ.Symbol]) {
			seen[occ.Symbol] = true
			kind := kinds[occ.Symbol]
			it := &item{
				symbol:   occ.Symbol,
				defLine:  line,
				lines:    []int{line},
				callable: isFunctionSymbol(occ.Symbol) || isCallableKind(kind),
				holder:   isTopLevelVariable(occ.Symbol, kind),
			}
			if it.holder {
				// A top-level variable's annotations and doc comment come
				// before its name (`@ProviderFor(SyncService)` above
				// `final syncServiceProvider`): take the unbroken run of
				// lines right above it from the declaration before.
				// dart format puts a closing brace on a line of its own,
				// which holds no occurrence and ends the run.
				keep, next := len(cur.lines), line-1
				for keep > 0 {
					l := cur.lines[keep-1]
					if l == next+1 || (l == next && l != cur.defLine && (cur.symbol == "" || l > cur.defLine)) {
						next = min(next, l-1)
						keep--
						continue
					}
					break
				}
				it.lines = append(append([]int(nil), cur.lines[keep:]...), it.lines...)
				cur.lines = cur.lines[:keep]
			}
			items = append(items, it)
			cur = it
			continue
		}
		cur.lines = append(cur.lines, line)
	}
	for _, it := range items {
		it.start, it.last = it.defLine, it.defLine
		for _, l := range it.lines {
			it.start, it.last = min(it.start, l), max(it.last, l)
		}
	}

	for i, it := range items {
		if !it.callable && !it.holder {
			continue
		}
		end, next := it.last, -1
		for _, later := range items[i+1:] {
			if !strings.HasPrefix(later.symbol, it.symbol) {
				next = later.start
				break
			}
			end = max(end, later.last) // a parameter default, a nested declaration
		}
		if next > end {
			end = next - 1
		} else if next < 0 && it.callable {
			end = max(end, it.defLine+DefaultMaxFunctionLines)
		}
		b := DefinitionBound{Symbol: it.symbol, DefLine: it.defLine, Start: it.start, End: end, Precision: BoundsInferred}
		if it.callable {
			funcs = append(funcs, b)
		} else {
			holders = append(holders, b)
		}
	}
	return funcs, holders
}

// isTopLevelVariable reports whether symbolID is a variable or constant
// declared directly in a file or package, not a member or a local.
func isTopLevelVariable(symbolID string, kind int32) bool {
	switch bindingscip.SymbolInformation_Kind(kind) {
	case bindingscip.SymbolInformation_Variable, bindingscip.SymbolInformation_Constant:
	default:
		return false
	}
	sym, err := bindingscip.ParseSymbol(symbolID)
	if err != nil || len(sym.Descriptors) == 0 {
		return false
	}
	n := len(sym.Descriptors)
	if sym.Descriptors[n-1].Suffix != bindingscip.Descriptor_Term {
		return false
	}
	for _, d := range sym.Descriptors[:n-1] {
		if d.Suffix != bindingscip.Descriptor_Namespace {
			return false
		}
	}
	return true
}

// holderBounds is the top-level variables' bounds for a document whose
// functions orderedBounds infers, nil for any other.
func holderBounds(doc *Document) []DefinitionBound {
	if !usesOrderedBounds(doc) {
		return nil
	}
	_, holders := orderedBounds(doc)
	return holders
}
