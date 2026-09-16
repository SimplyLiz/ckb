package scip

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/errors"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// OccurrenceRef is a lightweight reference to an occurrence for the inverted index
type OccurrenceRef struct {
	Doc *Document
	Occ *Occurrence
}

// NameEntry is a compact (name, symbolID) pair used in the sorted NameIndex.
//
// Alias is the container-qualified form ("Container.member") when the symbol
// has a container, mirroring the FTS signature alias in
// convertSymbolToFTSRecord — it lets a member stay findable by searching its
// container's name (e.g. "Handler" finds "Handler#handle") on the non-FTS
// SearchSymbols path too.
type NameEntry struct {
	Name  string
	Alias string
	ID    string
}

// SCIPIndex represents a loaded SCIP index
type SCIPIndex struct {
	// Metadata contains index metadata
	Metadata *Metadata

	// Documents are all indexed documents
	Documents []*Document

	// DocumentsByPath is an O(1) lookup map from relative path to document
	DocumentsByPath map[string]*Document

	// Symbols maps symbol IDs to symbol information
	Symbols map[string]*SymbolInformation

	// RefIndex is an inverted index for O(1) reference lookups: symbolId -> occurrences
	RefIndex map[string][]*OccurrenceRef

	// ConvertedSymbols caches pre-converted SCIPSymbol objects to avoid repeated conversion
	ConvertedSymbols map[string]*SCIPSymbol

	// ContainerIndex maps occurrence positions to their containing symbol for O(1) lookup
	// Key format: "docPath:line:col" -> containerSymbolId
	ContainerIndex map[string]string

	// DefinitionIndex maps symbolId to its single definition OccurrenceRef for O(1) lookup.
	// Built during the parallel doc phase alongside RefIndex.
	DefinitionIndex map[string]*OccurrenceRef

	// NameIndex is a sorted slice of (name, symbolId) pairs for cache-friendly search.
	// Sorted ascending by Name so binary search works for prefix queries.
	NameIndex []NameEntry

	// CallerIndex maps each callee symbolID to the slice of caller symbolIDs.
	// Populated lazily on the first FindCallers call via callerOnce.
	// FindCallers is O(1) via this index instead of the former O(docs×syms×occs) scan.
	CallerIndex     map[string][]string
	callerIndexOnce sync.Once

	// LoadedAt is when the index was loaded
	LoadedAt time.Time

	// IndexedCommit is the git commit the index was built from
	IndexedCommit string
}

// LoadSCIPIndex loads a SCIP index from the specified path.
func LoadSCIPIndex(path string) (*SCIPIndex, error) {
	return loadSCIPIndexInternal(path, "")
}

// loadSCIPIndexInternal is the implementation shared by LoadSCIPIndex and the
// cache-aware path used by SCIPAdapter.
func loadSCIPIndexInternal(path, cachePath string) (*SCIPIndex, error) {
	// Verify the file exists before mmap'ing.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, errors.NewCkbError(
			errors.IndexMissing,
			fmt.Sprintf("SCIP index not found at %s", path),
			err,
			errors.GetSuggestedFixes(errors.IndexMissing),
			nil,
		)
	}

	// Memory-map the file. On Unix this avoids copying the raw bytes onto the
	// Go heap: the OS manages paging and only pulls in pages that are actually
	// accessed during the protobuf parse below.
	data, cleanup, err := mapFile(path)
	if err != nil {
		return nil, errors.NewCkbError(
			errors.InternalError,
			fmt.Sprintf("Failed to read SCIP index from %s", path),
			err,
			nil,
			nil,
		)
	}
	defer cleanup()

	// ------------------------------------------------------------------ //
	// Phase 1: stream-parse the Index wire format document by document.   //
	// We never materialise a scippb.Index (which would hold all documents //
	// simultaneously). Instead, each scippb.Document is unmarshalled      //
	// individually, handed to a worker, then released.                    //
	// ------------------------------------------------------------------ //
	nWorkers := runtime.GOMAXPROCS(0)

	// DiscardUnknown skips unknown proto fields during decode, reducing
	// allocations from the reflection-based unknown-field accumulator.
	discardUnknownOpts := proto.UnmarshalOptions{DiscardUnknown: true}

	type docResult struct {
		doc              *Document
		symbols          map[string]*SymbolInformation
		refEntries       map[string][]*OccurrenceRef
		defEntries       map[string]*OccurrenceRef // first definition per symbol
		containerEntries map[string]string
	}

	// Producer: parses the outer Index message and sends each document to workers.
	type pbDocMsg struct{ doc *scippb.Document }
	jobs := make(chan pbDocMsg, nWorkers*2)

	var pbMeta *scippb.Metadata
	var parseErr error

	go func() {
		defer close(jobs)
		b := data
		for len(b) > 0 {
			num, typ, n := protowire.ConsumeTag(b)
			if n < 0 {
				parseErr = fmt.Errorf("protowire: invalid tag at offset %d", len(data)-len(b))
				return
			}
			b = b[n:]

			switch num {
			case 1: // Metadata
				v, n := protowire.ConsumeBytes(b)
				if n < 0 {
					b = b[max(n, 1):]
					continue
				}
				var m scippb.Metadata
				if discardUnknownOpts.Unmarshal(v, &m) == nil {
					pbMeta = &m
				}
				b = b[n:]

			case 2: // Document (protobuf:"bytes,2,rep,name=documents")
				v, n := protowire.ConsumeBytes(b)
				if n < 0 {
					b = b[max(n, 1):]
					continue
				}
				var d scippb.Document
				if discardUnknownOpts.Unmarshal(v, &d) == nil {
					jobs <- pbDocMsg{doc: &d}
				}
				b = b[n:]

			default: // external_symbols (field 3) or unknown fields — skip
				n := protowire.ConsumeFieldValue(num, typ, b)
				if n < 0 {
					b = b[max(n, 1):]
					continue
				}
				b = b[n:]
			}
		}
	}()

	// Consumers: convert each document and build per-doc indexes.
	var (
		results   []docResult
		resultsMu sync.Mutex
		wg        sync.WaitGroup
	)
	for w := 0; w < nWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range jobs {
				pbDoc := msg.doc
				doc := convertDocument(pbDoc)

				r := docResult{
					doc:              doc,
					symbols:          make(map[string]*SymbolInformation, len(doc.Symbols)),
					refEntries:       make(map[string][]*OccurrenceRef, len(doc.Occurrences)/4+1),
					defEntries:       make(map[string]*OccurrenceRef),
					containerEntries: make(map[string]string),
				}

				for _, sym := range doc.Symbols {
					r.symbols[sym.Symbol] = sym
				}

				// Pre-allocate one backing slice for all OccurrenceRefs in this
				// document. Taking pointers into a pre-sized slice replaces the
				// previous pattern of one heap allocation per occurrence, cutting
				// allocations from O(total_occs) — 68M at 50k docs — down to
				// O(docs) — 50k. The slice stays alive because the map values
				// hold pointers into it.
				backing := make([]OccurrenceRef, 0, len(doc.Occurrences))
				for i := range doc.Occurrences {
					occ := doc.Occurrences[i]
					if occ.Symbol == "" {
						continue
					}
					backing = append(backing, OccurrenceRef{Doc: doc, Occ: occ})
				}
				for i := range backing {
					ref := &backing[i]
					r.refEntries[ref.Occ.Symbol] = append(r.refEntries[ref.Occ.Symbol], ref)

					// Capture first definition occurrence for DefinitionIndex.
					if ref.Occ.SymbolRoles&SymbolRoleDefinition != 0 {
						if _, exists := r.defEntries[ref.Occ.Symbol]; !exists {
							r.defEntries[ref.Occ.Symbol] = ref
						}
					}
				}

				// ContainerIndex: sort def-scopes by size so first match = innermost.
				type defScope struct {
					symbol    string
					startLine int32
					endLine   int32
				}
				var defScopes []defScope
				for _, occ := range doc.Occurrences {
					if occ.SymbolRoles&SymbolRoleDefinition != 0 && len(occ.EnclosingRange) >= 3 {
						startLine := occ.EnclosingRange[0]
						endLine := startLine
						if len(occ.EnclosingRange) >= 4 {
							endLine = occ.EnclosingRange[2]
						}
						defScopes = append(defScopes, defScope{occ.Symbol, startLine, endLine})
					}
				}
				if len(defScopes) > 0 {
					sort.Slice(defScopes, func(a, b int) bool {
						return (defScopes[a].endLine - defScopes[a].startLine) <
							(defScopes[b].endLine - defScopes[b].startLine)
					})
					for _, occ := range doc.Occurrences {
						if len(occ.Range) < 2 {
							continue
						}
						occLine := occ.Range[0]
						for di := range defScopes {
							ds := &defScopes[di]
							if occLine >= ds.startLine && occLine <= ds.endLine {
								r.containerEntries[fmt.Sprintf("%s:%d:%d",
									doc.RelativePath, occ.Range[0], occ.Range[1])] = ds.symbol
								break
							}
						}
					}
				}

				resultsMu.Lock()
				results = append(results, r)
				resultsMu.Unlock()
			}
		}()
	}
	wg.Wait()

	if parseErr != nil {
		return nil, errors.NewCkbError(
			errors.InternalError,
			fmt.Sprintf("Failed to parse SCIP index from %s: %v", path, parseErr),
			parseErr,
			[]errors.FixAction{
				{
					Type:        errors.RunCommand,
					Command:     "scip print --index=" + path,
					Safe:        true,
					Description: "Verify SCIP index is valid",
				},
			},
			nil,
		)
	}

	// ------------------------------------------------------------------ //
	// Phase 2: merge per-doc results into the main index.                 //
	// ------------------------------------------------------------------ //

	// Sort results by document path so RefIndex / DefinitionIndex construction
	// is deterministic regardless of goroutine scheduling.
	sort.Slice(results, func(i, j int) bool {
		return results[i].doc.RelativePath < results[j].doc.RelativePath
	})

	totalSyms := 0
	totalRefs := 0
	totalContainer := 0
	docs := make([]*Document, 0, len(results))
	for _, r := range results {
		docs = append(docs, r.doc)
		totalSyms += len(r.symbols)
		totalRefs += len(r.refEntries)
		totalContainer += len(r.containerEntries)
	}

	scipIndex := &SCIPIndex{
		Metadata:         convertMetadata(pbMeta),
		Documents:        docs,
		DocumentsByPath:  make(map[string]*Document, len(docs)),
		Symbols:          make(map[string]*SymbolInformation, totalSyms),
		RefIndex:         make(map[string][]*OccurrenceRef, totalRefs),
		ConvertedSymbols: make(map[string]*SCIPSymbol, totalSyms),
		ContainerIndex:   make(map[string]string, totalContainer),
		DefinitionIndex:  make(map[string]*OccurrenceRef, totalSyms/2),
		LoadedAt:         time.Now(),
	}

	for _, doc := range docs {
		scipIndex.DocumentsByPath[doc.RelativePath] = doc
	}
	for _, r := range results {
		for k, v := range r.symbols {
			scipIndex.Symbols[k] = v
		}
		for k, v := range r.refEntries {
			scipIndex.RefIndex[k] = append(scipIndex.RefIndex[k], v...)
		}
		for k, v := range r.containerEntries {
			scipIndex.ContainerIndex[k] = v
		}
		for k, v := range r.defEntries {
			if _, exists := scipIndex.DefinitionIndex[k]; !exists {
				scipIndex.DefinitionIndex[k] = v
			}
		}
	}

	// Extract indexed commit from metadata.
	if scipIndex.Metadata != nil && scipIndex.Metadata.ToolInfo != nil {
		scipIndex.IndexedCommit = extractCommitFromToolInfo(scipIndex.Metadata.ToolInfo)
	}

	// ------------------------------------------------------------------ //
	// Phase 3: ConvertedSymbols + NameIndex.                              //
	// Check the derived cache first — if valid, skip the expensive        //
	// parallel symbol-conversion pass.                                    //
	// ------------------------------------------------------------------ //
	var cached *derivedCache
	if cachePath != "" {
		cached = loadDerivedCache(cachePath, path)
	}

	if cached != nil {
		// Fast path: restore from cache.
		applyCachedDerived(scipIndex, cached)
	} else {
		// Slow path: parallel symbol conversion.
		type symResult struct {
			id  string
			sym *SCIPSymbol
		}
		symIDs := make([]string, 0, len(scipIndex.Symbols))
		for id := range scipIndex.Symbols {
			symIDs = append(symIDs, id)
		}

		symCh := make(chan symResult, len(symIDs))
		batchSize := (len(symIDs) + nWorkers - 1) / nWorkers
		if batchSize < 1 {
			batchSize = 1
		}

		var wg2 sync.WaitGroup
		for b := 0; b*batchSize < len(symIDs); b++ {
			start := b * batchSize
			end := start + batchSize
			if end > len(symIDs) {
				end = len(symIDs)
			}
			wg2.Add(1)
			go func(ids []string) {
				defer wg2.Done()
				for _, id := range ids {
					if converted, err := convertToSCIPSymbol(scipIndex.Symbols[id], scipIndex); err == nil {
						symCh <- symResult{id: id, sym: converted}
					}
				}
			}(symIDs[start:end])
		}
		go func() {
			wg2.Wait()
			close(symCh)
		}()
		for r := range symCh {
			scipIndex.ConvertedSymbols[r.id] = r.sym
		}

		// Build NameIndex: sorted (name, id) pairs for cache-friendly search.
		// Sort by (Name, ID) to get a total order — equal names would otherwise
		// produce non-deterministic output since the map iteration order is random.
		nameIdx := make([]NameEntry, 0, len(scipIndex.ConvertedSymbols))
		for id, sym := range scipIndex.ConvertedSymbols {
			alias := ""
			if sym.ContainerName != "" {
				alias = sym.ContainerName + "." + sym.Name
			}
			nameIdx = append(nameIdx, NameEntry{Name: sym.Name, Alias: alias, ID: id})
		}
		sort.Slice(nameIdx, func(a, b int) bool {
			if nameIdx[a].Name != nameIdx[b].Name {
				return nameIdx[a].Name < nameIdx[b].Name
			}
			return nameIdx[a].ID < nameIdx[b].ID
		})
		scipIndex.NameIndex = nameIdx

		// Persist to cache for next startup.
		if cachePath != "" {
			go saveDerivedCache(cachePath, scipIndex, path)
		}
	}

	// Phase 4 (CallerIndex) is built lazily on the first FindCallers call.
	// This keeps load time clean and avoids ~22k persistent heap objects on
	// small indexes that would otherwise inflate GC pressure at startup.

	return scipIndex, nil
}

// max returns the larger of a and b (int).
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// IsStale checks if the index is stale compared to the current HEAD commit
func (i *SCIPIndex) IsStale(headCommit string) bool {
	// If we don't know the indexed commit, assume it's stale
	if i.IndexedCommit == "" {
		return true
	}

	// Compare with HEAD
	return i.IndexedCommit != headCommit
}

// GetDocument retrieves a document by its relative path
func (i *SCIPIndex) GetDocument(relativePath string) *Document {
	return i.DocumentsByPath[relativePath]
}

// GetSymbol retrieves symbol information by ID
func (i *SCIPIndex) GetSymbol(symbolId string) *SymbolInformation {
	return i.Symbols[symbolId]
}

// AllSymbols returns all symbols in the index
func (i *SCIPIndex) AllSymbols() []*SymbolInformation {
	symbols := make([]*SymbolInformation, 0, len(i.Symbols))
	for _, sym := range i.Symbols {
		symbols = append(symbols, sym)
	}
	return symbols
}

// convertMetadata converts protobuf metadata to internal representation
func convertMetadata(meta *scippb.Metadata) *Metadata {
	if meta == nil {
		return nil
	}

	var toolInfo *ToolInfo
	if meta.ToolInfo != nil {
		toolInfo = &ToolInfo{
			Name:      meta.ToolInfo.Name,
			Version:   meta.ToolInfo.Version,
			Arguments: meta.ToolInfo.Arguments,
		}
	}

	return &Metadata{
		Version:              fmt.Sprintf("%d", meta.Version),
		ToolInfo:             toolInfo,
		ProjectRoot:          meta.ProjectRoot,
		TextDocumentEncoding: meta.TextDocumentEncoding.String(),
	}
}

// convertDocument converts a single protobuf document
func convertDocument(doc *scippb.Document) *Document {
	occurrences := make([]*Occurrence, len(doc.Occurrences))
	for i, occ := range doc.Occurrences {
		occurrences[i] = convertOccurrence(occ)
	}

	symbols := make([]*SymbolInformation, len(doc.Symbols))
	for i, sym := range doc.Symbols {
		symbols[i] = convertSymbolInformation(sym)
	}

	return &Document{
		RelativePath: doc.RelativePath,
		Language:     doc.Language,
		Occurrences:  occurrences,
		Symbols:      symbols,
	}
}

// convertOccurrence converts a protobuf occurrence
func convertOccurrence(occ *scippb.Occurrence) *Occurrence {
	diagnostics := make([]*Diagnostic, len(occ.Diagnostics))
	for i, diag := range occ.Diagnostics {
		// Convert DiagnosticTag slice to int32 slice
		tags := make([]int32, len(diag.Tags))
		for j, tag := range diag.Tags {
			tags[j] = int32(tag)
		}

		diagnostics[i] = &Diagnostic{
			Severity: int32(diag.Severity),
			Code:     diag.Code,
			Message:  diag.Message,
			Source:   diag.Source,
			Tags:     tags,
		}
	}

	return &Occurrence{
		Range:                 occ.Range,
		Symbol:                occ.Symbol,
		SymbolRoles:           occ.SymbolRoles,
		OverrideDocumentation: occ.OverrideDocumentation,
		SyntaxKind:            int32(occ.SyntaxKind),
		Diagnostics:           diagnostics,
		EnclosingRange:        occ.EnclosingRange,
	}
}

// convertSymbolInformation converts protobuf symbol information
func convertSymbolInformation(sym *scippb.SymbolInformation) *SymbolInformation {
	relationships := make([]*Relationship, len(sym.Relationships))
	for i, rel := range sym.Relationships {
		relationships[i] = &Relationship{
			Symbol:           rel.Symbol,
			IsReference:      rel.IsReference,
			IsImplementation: rel.IsImplementation,
			IsTypeDefinition: rel.IsTypeDefinition,
			IsDefinition:     rel.IsDefinition,
		}
	}

	return &SymbolInformation{
		Symbol:                 sym.Symbol,
		Documentation:          sym.Documentation,
		Relationships:          relationships,
		Kind:                   int32(sym.Kind),
		DisplayName:            sym.DisplayName,
		SignatureDocumentation: nil, // Skip signature documentation for now
		EnclosingSymbol:        sym.EnclosingSymbol,
	}
}

// extractCommitFromToolInfo attempts to extract git commit from tool info
func extractCommitFromToolInfo(toolInfo *ToolInfo) string {
	// Look for commit hash in arguments
	// Common patterns:
	// --commit=<hash>
	// --git-commit=<hash>
	// --module-version=<hash> (scip-go)
	// -c <hash>
	for i, arg := range toolInfo.Arguments {
		if len(arg) > 9 && arg[:9] == "--commit=" {
			return arg[9:]
		}
		if len(arg) > 13 && arg[:13] == "--git-commit=" {
			return arg[13:]
		}
		if len(arg) > 17 && arg[:17] == "--module-version=" {
			return arg[17:]
		}
		if arg == "-c" && i+1 < len(toolInfo.Arguments) {
			return toolInfo.Arguments[i+1]
		}
	}

	// Also check version field which scip-go populates
	if toolInfo.Version != "" && looksLikeCommitHash(toolInfo.Version) {
		return toolInfo.Version
	}

	return ""
}

// looksLikeCommitHash checks if a string looks like a git commit hash
func looksLikeCommitHash(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// GetIndexPath returns the index path from config and repo root
func GetIndexPath(repoRoot string, configPath string) string {
	if filepath.IsAbs(configPath) {
		return configPath
	}
	return filepath.Join(repoRoot, configPath)
}
