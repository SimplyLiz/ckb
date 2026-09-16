package scip

import (
	"sort"
	"strings"
)

// DefaultMaxFunctionLines is the default maximum function length (in lines) used
// when we can't determine the actual function boundary. This is a heuristic
// workaround because scip-go doesn't populate EnclosingRange for functions.
// This value is conservative to avoid missing callees in long functions.
const DefaultMaxFunctionLines = 500

// CallGraphNode represents a node in the call graph
type CallGraphNode struct {
	SymbolID string
	Name     string
	Kind     SymbolKind
	Location *Location
}

// CallGraphEdge represents an edge in the call graph
type CallGraphEdge struct {
	From string // caller symbol ID
	To   string // callee symbol ID
	Kind string // "call" or "reference"
}

// CallGraph represents a call graph centered on a symbol
type CallGraph struct {
	Root    *CallGraphNode
	Nodes   map[string]*CallGraphNode
	Edges   []CallGraphEdge
	Callers []*CallGraphNode // symbols that call the root
	Callees []*CallGraphNode // symbols that the root calls
}

// CallGraphDirection specifies which direction to traverse
type CallGraphDirection string

const (
	DirectionCallers CallGraphDirection = "callers"
	DirectionCallees CallGraphDirection = "callees"
	DirectionBoth    CallGraphDirection = "both"
)

// CallGraphOptions contains options for building call graphs
type CallGraphOptions struct {
	Direction CallGraphDirection
	MaxDepth  int
	MaxNodes  int
}

// FindCallees finds all symbols called by the given function
func (idx *SCIPIndex) FindCallees(symbolId string) ([]*CallGraphNode, error) {
	callees := make([]*CallGraphNode, 0)
	seen := make(map[string]bool)

	// Find the function's definition and its document
	var funcDoc *Document
	var funcDefLine int = -1

	for _, doc := range idx.Documents {
		for _, occ := range doc.Occurrences {
			if occ.Symbol == symbolId && occ.SymbolRoles&SymbolRoleDefinition != 0 {
				funcDoc = doc
				funcDefLine = int(occ.Range[0]) // #nosec G115 -- SCIP int32 fits in int
				break
			}
		}
		if funcDoc != nil {
			break
		}
	}

	if funcDoc == nil || funcDefLine < 0 {
		// No definition found
		return callees, nil
	}

	// Build function ranges to determine where this function ends
	funcRanges := buildFunctionRanges(funcDoc)
	funcRange, found := funcRanges[symbolId]
	if !found {
		// Function not in range map, create a default range
		funcRange = lineRange{start: funcDefLine, end: funcDefLine + DefaultMaxFunctionLines}
	}

	// Search for all function references within this function's body
	for _, occ := range funcDoc.Occurrences {
		// Skip the function definition itself
		if occ.Symbol == symbolId {
			continue
		}

		// Skip definitions (we want references/calls)
		if occ.SymbolRoles&SymbolRoleDefinition != 0 {
			continue
		}

		occLine := int(occ.Range[0]) // #nosec G115 -- SCIP int32 fits in int

		// Check if this occurrence is within the function's body
		if occLine < funcRange.start || occLine > funcRange.end {
			continue
		}

		// Skip if already seen
		if seen[occ.Symbol] {
			continue
		}

		// Check if the target is a callable (function, method)
		// Use isFunctionSymbol since scip-go doesn't populate Kind
		if isFunctionSymbol(occ.Symbol) {
			seen[occ.Symbol] = true

			kind := KindFunction
			if symInfo := idx.GetSymbol(occ.Symbol); symInfo != nil {
				if k := mapSCIPKind(symInfo.Kind); k != KindUnknown {
					kind = k
				}
			}

			location := findSymbolLocation(occ.Symbol, idx)
			callees = append(callees, &CallGraphNode{
				SymbolID: occ.Symbol,
				Name:     extractSymbolName(occ.Symbol),
				Kind:     kind,
				Location: location,
			})
		}
	}

	return callees, nil
}

// FindCallers finds all functions that call the given symbol.
// CallerIndex is built lazily on the first call and reused thereafter.
func (idx *SCIPIndex) FindCallers(symbolId string) ([]*CallGraphNode, error) {
	idx.callerIndexOnce.Do(func() {
		idx.CallerIndex = buildCallerIndex(idx.Documents)
	})
	callerIDs := idx.CallerIndex[symbolId]
	if len(callerIDs) == 0 {
		return []*CallGraphNode{}, nil
	}
	callers := make([]*CallGraphNode, 0, len(callerIDs))
	for _, callerID := range callerIDs {
		symInfo := idx.GetSymbol(callerID)
		kind := KindFunction
		if symInfo != nil {
			if k := mapSCIPKind(symInfo.Kind); k != KindUnknown {
				kind = k
			}
		}
		callers = append(callers, &CallGraphNode{
			SymbolID: callerID,
			Name:     extractSymbolName(callerID),
			Kind:     kind,
			Location: findSymbolLocation(callerID, idx),
		})
	}
	return callers, nil
}

// buildCallerIndex constructs the CallerIndex from all documents.
// For every non-definition occurrence of a symbol that falls within a function
// body, records that function as a caller of that symbol.
// Result: callee symbolID → []caller symbolIDs (deduplicated per document).
// Called once during LoadIndex; FindCallers is O(1) via this map.
func buildCallerIndex(docs []*Document) map[string][]string {
	callerIdx := make(map[string][]string, len(docs))

	// interval is declared outside the loop so ivs can be reused across docs.
	type interval struct {
		symbol string
		start  int
		end    int
	}
	type edge struct{ callee, caller string }

	// Reuse ivs across docs — resliced to 0, grown only when needed.
	var ivs []interval
	// Reuse docSeen via a generation counter — no per-doc alloc or clear.
	docSeen := make(map[edge]uint64, 64)
	var gen uint64

	for _, doc := range docs {
		funcRanges := buildFunctionRanges(doc)
		if len(funcRanges) == 0 {
			continue
		}

		// Sort intervals by start line for early-break during occurrence scan.
		ivs = ivs[:0]
		if cap(ivs) < len(funcRanges) {
			ivs = make([]interval, 0, len(funcRanges))
		}
		for sym, lr := range funcRanges {
			ivs = append(ivs, interval{sym, lr.start, lr.end})
		}
		sort.Slice(ivs, func(i, j int) bool { return ivs[i].start < ivs[j].start })

		// Bump generation to logically clear docSeen without re-allocating.
		gen++

		for _, occ := range doc.Occurrences {
			if occ.Symbol == "" || occ.SymbolRoles&SymbolRoleDefinition != 0 {
				continue
			}
			occLine := int(occ.Range[0]) // #nosec G115 -- SCIP int32 fits in int
			for _, iv := range ivs {
				if occLine < iv.start {
					break // ivs sorted by start; no later interval can contain occLine
				}
				if occLine <= iv.end {
					callee := occ.Symbol
					caller := iv.symbol
					if caller == callee {
						break // skip self-references
					}
					e := edge{callee, caller}
					if docSeen[e] != gen {
						docSeen[e] = gen
						callerIdx[callee] = append(callerIdx[callee], caller)
					}
					break
				}
			}
		}
	}

	return callerIdx
}

// lineRange represents a start and end line for a function
type lineRange struct {
	start int
	end   int
}

// buildFunctionRanges builds a map of function symbol -> line range for a document
// It infers function end lines by looking at the next function's start line
func buildFunctionRanges(doc *Document) map[string]lineRange {
	ranges := make(map[string]lineRange)

	// Collect all function definition locations
	type funcDef struct {
		symbol    string
		startLine int
	}
	var funcs []funcDef

	for _, sym := range doc.Symbols {
		// Detect functions from symbol ID format since scip-go doesn't set Kind
		// Functions have () in the symbol ID, e.g., "...`pkg`/FuncName()."
		if !isFunctionSymbol(sym.Symbol) && mapSCIPKind(sym.Kind) != KindFunction && mapSCIPKind(sym.Kind) != KindMethod {
			continue
		}

		// Find the definition occurrence for this symbol
		for _, occ := range doc.Occurrences {
			if occ.Symbol == sym.Symbol && occ.SymbolRoles&SymbolRoleDefinition != 0 {
				funcs = append(funcs, funcDef{
					symbol:    sym.Symbol,
					startLine: int(occ.Range[0]), // #nosec G115 -- SCIP int32 fits in int
				})
				break
			}
		}
	}

	// Sort by start line
	sort.Slice(funcs, func(i, j int) bool {
		return funcs[i].startLine < funcs[j].startLine
	})

	// Assign end lines (next function's start - 1, or a reasonable default)
	for i, f := range funcs {
		endLine := f.startLine + DefaultMaxFunctionLines
		if i+1 < len(funcs) {
			endLine = funcs[i+1].startLine - 1
		}
		ranges[f.symbol] = lineRange{start: f.startLine, end: endLine}
	}

	return ranges
}

// isFunctionSymbol detects if a SCIP symbol ID represents a function/method.
// This is a heuristic workaround because scip-go doesn't populate the Kind field.
// scip-go uses format like: scip-go gomod mod version `pkg`/FuncName().
// Functions have "()" before the final "." in the descriptor.
//
// Examples:
//   - "scip-go go ckb/internal/query NewEngine()." → function (has "()")
//   - "scip-go go ckb/internal/query Engine#Close()." → method (has "()")
//   - "scip-go go ckb/internal/query Engine#" → type (no "()")
//   - "scip-go go ckb/internal/query Engine#logger." → field (no "()")
//
// Note: We infer function-ness from the symbol ID because scip-go does not
// reliably populate sym.Kind for all symbol types.
func isFunctionSymbol(symbolId string) bool {
	return strings.Contains(symbolId, "().")
}

// BuildCallGraph builds a call graph using bounded BFS
func (idx *SCIPIndex) BuildCallGraph(symbolId string, opts CallGraphOptions) (*CallGraph, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 1
	}
	if opts.MaxDepth > 4 {
		opts.MaxDepth = 4 // Hard limit
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 100
	}

	graph := &CallGraph{
		Nodes:   make(map[string]*CallGraphNode),
		Edges:   make([]CallGraphEdge, 0),
		Callers: make([]*CallGraphNode, 0),
		Callees: make([]*CallGraphNode, 0),
	}

	// Track seen edges to avoid duplicates (key: "from->to")
	seenEdges := make(map[string]bool)

	// Create root node
	rootInfo := idx.GetSymbol(symbolId)
	rootLocation := findSymbolLocation(symbolId, idx)
	rootKind := KindFunction
	if rootInfo != nil {
		rootKind = mapSCIPKind(rootInfo.Kind)
	}

	graph.Root = &CallGraphNode{
		SymbolID: symbolId,
		Name:     extractSymbolName(symbolId),
		Kind:     rootKind,
		Location: rootLocation,
	}
	graph.Nodes[symbolId] = graph.Root

	// BFS for callers
	if opts.Direction == DirectionCallers || opts.Direction == DirectionBoth {
		visited := make(map[string]bool)
		visited[symbolId] = true

		queue := []struct {
			id    string
			depth int
		}{{symbolId, 0}}

		for len(queue) > 0 && len(graph.Nodes) < opts.MaxNodes {
			current := queue[0]
			queue = queue[1:]

			if current.depth >= opts.MaxDepth {
				continue
			}

			callers, err := idx.FindCallers(current.id)
			if err != nil {
				continue
			}

			for _, caller := range callers {
				// Add edge if not already seen
				edgeKey := caller.SymbolID + "->" + current.id
				if !seenEdges[edgeKey] {
					seenEdges[edgeKey] = true
					graph.Edges = append(graph.Edges, CallGraphEdge{
						From: caller.SymbolID,
						To:   current.id,
						Kind: "call",
					})
				}

				// Add to direct callers list if at depth 1 from root
				if current.id == symbolId {
					graph.Callers = append(graph.Callers, caller)
				}

				// Add node if not exists
				if _, exists := graph.Nodes[caller.SymbolID]; !exists {
					graph.Nodes[caller.SymbolID] = caller
				}

				// Queue for further exploration
				if !visited[caller.SymbolID] {
					visited[caller.SymbolID] = true
					queue = append(queue, struct {
						id    string
						depth int
					}{caller.SymbolID, current.depth + 1})
				}
			}
		}
	}

	// BFS for callees
	if opts.Direction == DirectionCallees || opts.Direction == DirectionBoth {
		visited := make(map[string]bool)
		visited[symbolId] = true

		queue := []struct {
			id    string
			depth int
		}{{symbolId, 0}}

		for len(queue) > 0 && len(graph.Nodes) < opts.MaxNodes {
			current := queue[0]
			queue = queue[1:]

			if current.depth >= opts.MaxDepth {
				continue
			}

			callees, err := idx.FindCallees(current.id)
			if err != nil {
				continue
			}

			for _, callee := range callees {
				// Add edge if not already seen
				edgeKey := current.id + "->" + callee.SymbolID
				if !seenEdges[edgeKey] {
					seenEdges[edgeKey] = true
					graph.Edges = append(graph.Edges, CallGraphEdge{
						From: current.id,
						To:   callee.SymbolID,
						Kind: "call",
					})
				}

				// Add to direct callees list if at depth 1 from root
				if current.id == symbolId {
					graph.Callees = append(graph.Callees, callee)
				}

				// Add node if not exists
				if _, exists := graph.Nodes[callee.SymbolID]; !exists {
					graph.Nodes[callee.SymbolID] = callee
				}

				// Queue for further exploration
				if !visited[callee.SymbolID] {
					visited[callee.SymbolID] = true
					queue = append(queue, struct {
						id    string
						depth int
					}{callee.SymbolID, current.depth + 1})
				}
			}
		}
	}

	return graph, nil
}

// GetCallerCount returns the number of unique callers for a symbol
func (idx *SCIPIndex) GetCallerCount(symbolId string) int {
	callers, err := idx.FindCallers(symbolId)
	if err != nil {
		return 0
	}
	return len(callers)
}

// GetCalleeCount returns the number of unique callees for a symbol
func (idx *SCIPIndex) GetCalleeCount(symbolId string) int {
	callees, err := idx.FindCallees(symbolId)
	if err != nil {
		return 0
	}
	return len(callees)
}

// GetReferenceCount returns the total number of references to a symbol
func (idx *SCIPIndex) GetReferenceCount(symbolId string) int {
	refs, err := idx.FindReferences(symbolId, ReferenceOptions{IncludeDefinition: false})
	if err != nil {
		return 0
	}
	return len(refs)
}

// mapSCIPKind maps SCIP kind codes to SymbolKind
func mapSCIPKind(kind int32) SymbolKind {
	// SCIP SymbolInformation.Kind values
	switch kind {
	case 1: // UnspecifiedSymbol
		return KindUnknown
	case 2: // Comment
		return KindUnknown
	case 3: // Package
		return KindPackage
	case 4: // PackageObject
		return KindModule
	case 5: // Class
		return KindClass
	case 6: // Object
		return KindClass
	case 7: // Trait
		return KindInterface
	case 8: // TraitMethod
		return KindMethod
	case 9: // Method
		return KindMethod
	case 10: // Macro
		return KindFunction
	case 11: // Type
		return KindType
	case 12: // Parameter
		return KindParameter
	case 13: // SelfParameter
		return KindParameter
	case 14: // TypeParameter
		return KindType
	case 15: // Local
		return KindVariable
	case 16: // Field
		return KindField
	case 17: // Interface
		return KindInterface
	case 18: // Function
		return KindFunction
	case 19: // Variable
		return KindVariable
	case 20: // Constant
		return KindConstant
	case 21: // String
		return KindConstant
	case 22: // Number
		return KindConstant
	case 23: // Boolean
		return KindConstant
	case 24: // Array
		return KindVariable
	case 25: // Namespace
		return KindNamespace
	case 26: // Null
		return KindConstant
	case 27: // Property
		return KindProperty
	case 28: // Enum
		return KindEnum
	case 29: // EnumMember
		return KindConstant
	case 30: // Struct
		return KindClass
	case 31: // Event
		return KindFunction
	case 32: // Operator
		return KindFunction
	case 33: // Constructor
		return KindMethod
	case 34: // Destructor
		return KindMethod
	default:
		return KindUnknown
	}
}

// extractSymbolName extracts a human-readable name from a SCIP symbol ID
func extractSymbolName(symbolId string) string {
	// SCIP symbol format: scheme ' ' package ' ' descriptor
	// Example: "go local ... func NewEngine"
	// We want to extract the last meaningful part

	parts := strings.Split(symbolId, " ")
	if len(parts) == 0 {
		return symbolId
	}

	// Find the last descriptor that contains a name
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		// Look for common Go patterns
		if strings.HasSuffix(part, "().") {
			// Method: "receiver.Method()."
			name := strings.TrimSuffix(part, "().")
			return stripQuotedPackagePrefix(name)
		}
		if strings.HasSuffix(part, ".") && !strings.HasSuffix(part, "().") {
			// Type or package
			name := strings.TrimSuffix(part, ".")
			return stripQuotedPackagePrefix(name)
		}
	}

	// Fallback: return the last non-empty part
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			name := strings.TrimSuffix(strings.TrimSuffix(parts[i], "."), "()")
			return stripQuotedPackagePrefix(name)
		}
	}

	return symbolId
}

// stripQuotedPackagePrefix removes a scip-go backtick-quoted package-path
// prefix from a descriptor fragment, e.g.
// "`github.com/org/repo/pkg`/Type#Method" -> "Type#Method". Unlike
// SCIPIdentifier.GetSimpleName, this intentionally keeps a "Type#Method" or
// "Type#field" receiver prefix — extractSymbolName's callers (call graph
// node names) want that context, not the bare method name.
//
// When there's no quoted package path, falls back to taking the text after
// the last '.' — but only then: scip-go module paths themselves contain
// dots (e.g. "github.com"), so blindly splitting on the last '.' of a
// backtick-quoted descriptor mistakes part of the module path for a name
// separator (e.g. "github.com/org/repo/pkg`/Type#Method" was truncated to
// "com/org/repo/pkg`/Type#Method" — the "github." prefix silently dropped).
func stripQuotedPackagePrefix(name string) string {
	if lastBacktick := strings.LastIndex(name, "`"); lastBacktick != -1 {
		remainder := name[lastBacktick+1:]
		if slashIdx := strings.Index(remainder, "/"); slashIdx != -1 {
			return remainder[slashIdx+1:]
		}
		return remainder
	}
	if lastDot := strings.LastIndex(name, "."); lastDot >= 0 {
		return name[lastDot+1:]
	}
	return name
}

// Note: findSymbolLocation and parseOccurrenceRange are defined in symbols.go
