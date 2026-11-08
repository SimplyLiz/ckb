package scip

import (
	"encoding/gob"
	"os"
	"time"
)

// DerivedCacheVersion identifies the derivation logic behind everything
// stored in the derived cache and in the FTS content (symbol kind, container
// name, alias, display name). Bump it whenever the way those are computed from
// a SCIP index changes, so installs upgrading in place rebuild them
// automatically. History: 1 = first versioned release (container-qualified
// Alias in NameIndex, descriptor-aware kinds, rust-analyzer impl blocks).
// 2 = local variables and parameters no longer count as containers in
// ContainerIndex. 3 = functions with inferred extents count as containers for
// indexers without EnclosingRange.
const DerivedCacheVersion = 3

// derivedCache is the on-disk representation of the expensive derived indexes.
// It is keyed by the .scip file's mtime+size so it is invalidated automatically
// whenever the index file changes.
//
// Cached: ConvertedSymbols, ContainerIndex, NameIndex.
// Not cached: Documents, Symbols, RefIndex, DefinitionIndex — these are rebuilt
// in the parallel streaming phase and are needed for pointer-based queries.
type derivedCache struct {
	// Version is DerivedCacheVersion at write time. A file written by a
	// release with different derivation logic (gob leaves the field zero for
	// files that predate it) is discarded and rebuilt instead of being
	// trusted: the cache is keyed on the .scip file's mtime+size only, which
	// does not change when ckb is upgraded.
	Version int

	ScipModTime int64 // UnixNano
	ScipSize    int64

	// flatSymbols stores a serializable version of ConvertedSymbols.
	FlatSymbols map[string]flatCachedSymbol

	// ContainerIndex is already map[string]string — stored directly.
	ContainerIndex map[string]string

	// NameIndex is already []NameEntry — stored directly.
	NameIndex []NameEntry
}

// flatCachedSymbol is a pointer-free version of SCIPSymbol for gob encoding.
type flatCachedSymbol struct {
	Name          string
	Kind          string
	Documentation string
	Modifiers     []string
	ContainerName string
	Visibility    string
	// Location fields inlined to avoid *Location pointer issues with gob.
	HasLocation bool
	LocPath     string
	LocLine     int
	LocCol      int
	LocEndLine  int
	LocEndCol   int
}

func init() {
	gob.Register(flatCachedSymbol{})
	gob.Register(NameEntry{})
}

// loadDerivedCache loads the derived cache for the given .scip file.
// Returns nil if the cache is missing, stale, or corrupt.
func loadDerivedCache(cachePath, scipPath string) *derivedCache {
	fi, err := os.Stat(scipPath)
	if err != nil {
		return nil
	}
	scipMtime := fi.ModTime().UnixNano()
	scipSize := fi.Size()

	f, err := os.Open(cachePath)
	if err != nil {
		return nil // cache does not exist yet
	}
	defer f.Close()

	var c derivedCache
	if err := gob.NewDecoder(f).Decode(&c); err != nil {
		return nil // corrupt
	}
	if c.Version != DerivedCacheVersion {
		return nil // written by a release with different derivation logic
	}
	if c.ScipModTime != scipMtime || c.ScipSize != scipSize {
		return nil // stale
	}
	return &c
}

// saveDerivedCache writes the derived cache to disk. Errors are ignored —
// a missing cache file just means the next startup does a full rebuild.
func saveDerivedCache(cachePath string, idx *SCIPIndex, scipPath string) {
	fi, err := os.Stat(scipPath)
	if err != nil {
		return
	}

	flat := make(map[string]flatCachedSymbol, len(idx.ConvertedSymbols))
	for id, sym := range idx.ConvertedSymbols {
		f := flatCachedSymbol{
			Name:          sym.Name,
			Kind:          string(sym.Kind),
			Documentation: sym.Documentation,
			ContainerName: sym.ContainerName,
			Visibility:    sym.Visibility,
		}
		if len(sym.Modifiers) > 0 {
			f.Modifiers = append([]string(nil), sym.Modifiers...)
		}
		if sym.Location != nil {
			f.HasLocation = true
			f.LocPath = sym.Location.FileId
			f.LocLine = sym.Location.StartLine
			f.LocCol = sym.Location.StartColumn
			f.LocEndLine = sym.Location.EndLine
			f.LocEndCol = sym.Location.EndColumn
		}
		flat[id] = f
	}

	c := derivedCache{
		Version:        DerivedCacheVersion,
		ScipModTime:    fi.ModTime().UnixNano(),
		ScipSize:       fi.Size(),
		FlatSymbols:    flat,
		ContainerIndex: idx.ContainerIndex,
		NameIndex:      idx.NameIndex,
	}

	// Write to a temp file and rename to avoid partial writes.
	tmp := cachePath + ".tmp." + time.Now().Format("20060102150405")
	fw, err := os.Create(tmp)
	if err != nil {
		return
	}
	if err := gob.NewEncoder(fw).Encode(&c); err != nil {
		fw.Close()
		os.Remove(tmp)
		return
	}
	fw.Close()
	os.Rename(tmp, cachePath) //nolint:errcheck
}

// applyCachedDerived merges cached derived data into an otherwise fully-built index.
// The caller must have already populated Documents, Symbols, RefIndex, DefinitionIndex.
func applyCachedDerived(idx *SCIPIndex, c *derivedCache) {
	// Restore ContainerIndex.
	idx.ContainerIndex = c.ContainerIndex

	// Restore ConvertedSymbols from flat representation.
	idx.ConvertedSymbols = make(map[string]*SCIPSymbol, len(c.FlatSymbols))
	for id, f := range c.FlatSymbols {
		sym := &SCIPSymbol{
			StableId:      id,
			Name:          f.Name,
			Kind:          SymbolKind(f.Kind),
			Documentation: f.Documentation,
			ContainerName: f.ContainerName,
			Visibility:    f.Visibility,
		}
		if len(f.Modifiers) > 0 {
			sym.Modifiers = f.Modifiers
		}
		if f.HasLocation {
			sym.Location = &Location{
				FileId:      f.LocPath,
				StartLine:   f.LocLine,
				StartColumn: f.LocCol,
				EndLine:     f.LocEndLine,
				EndColumn:   f.LocEndCol,
			}
		}
		idx.ConvertedSymbols[id] = sym
	}

	// Restore NameIndex.
	idx.NameIndex = c.NameIndex
}
