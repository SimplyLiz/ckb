// Package overlay patches a loaded SCIP index with the working tree's changes
// since the indexed commit, so answers stay right between full reindexes.
//
// Layer 0 (this file and remap.go) is exact and language-independent: from
// the diff hunks it computes where every unchanged old line is in the new
// file, moves the occurrences on those lines there, and drops the ones on
// changed lines. See docs/plans/index-overlay.md.
package overlay

import (
	"sort"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// block is one run of removed and added lines, in 1-based lines: removed old
// lines [Old, Old+Removed), added new lines [New, New+Added). A side with no
// lines marks the position before which the other side's lines sit.
type block struct {
	Old, Removed int
	New, Added   int
}

// LineMap maps old-file lines to new-file lines for one file.
type LineMap struct {
	blocks []block // sorted by Old
}

// NewLineMap builds the map from a file's hunks. It uses the hunks' Blocks
// when the diff parser set them, else their Removed and Added line lists.
func NewLineMap(hunks []impact.ChangedHunk) *LineMap {
	lm := &LineMap{}
	// Line lists are absolute, so the hunks without Blocks are rebuilt
	// together: their offsets add up from the top of the file.
	var removed, added []int
	for _, h := range hunks {
		if len(h.Blocks) > 0 {
			for _, b := range h.Blocks {
				lm.blocks = append(lm.blocks, block{Old: b.OldLine, Removed: len(b.Removed), New: b.NewLine, Added: len(b.Added)})
			}
			continue
		}
		removed = append(removed, h.Removed...)
		added = append(added, h.Added...)
	}
	lm.blocks = append(lm.blocks, blocksFromLines(removed, added)...)
	sort.SliceStable(lm.blocks, func(i, j int) bool { return lm.blocks[i].Old < lm.blocks[j].Old })
	return lm
}

// blocksFromLines rebuilds the blocks of a file from its removed (old-file)
// and added (new-file) line numbers, walking both files from line 1.
func blocksFromLines(removed, added []int) []block {
	r := append([]int(nil), removed...)
	a := append([]int(nil), added...)
	sort.Ints(r)
	sort.Ints(a)
	var out []block
	i, j := 0, 0
	o, n := 1, 1
	for i < len(r) || j < len(a) {
		if (i < len(r) && r[i] == o) || (j < len(a) && a[j] == n) {
			b := block{Old: o, New: n}
			for i < len(r) && r[i] == o {
				b.Removed++
				i++
				o++
			}
			for j < len(a) && a[j] == n {
				b.Added++
				j++
				n++
			}
			out = append(out, b)
			continue
		}
		k := -1
		if i < len(r) {
			k = r[i] - o
		}
		if j < len(a) && (k < 0 || a[j]-n < k) {
			k = a[j] - n
		}
		if k <= 0 {
			break // inconsistent input; stop rather than loop
		}
		o += k
		n += k
	}
	return out
}

// Empty reports whether the map has no changes (a pure rename).
func (lm *LineMap) Empty() bool { return len(lm.blocks) == 0 }

// Map returns the new line of an unchanged old line (both 1-based), or false
// when the old line was removed or replaced.
func (lm *LineMap) Map(old int) (int, bool) {
	delta := 0
	for _, b := range lm.blocks {
		if b.Old > old {
			break
		}
		if old < b.Old+b.Removed {
			return 0, false
		}
		delta += b.Added - b.Removed
	}
	return old + delta, true
}

// Unchanged reports whether old lines first..last (1-based, inclusive) are
// all unchanged and still adjacent: no line in between was removed and no
// line was inserted between two of them.
func (lm *LineMap) Unchanged(first, last int) bool {
	for _, b := range lm.blocks {
		if b.Old > last {
			break
		}
		if b.Removed > 0 && b.Old+b.Removed-1 >= first {
			return false
		}
		if b.Removed == 0 && b.Old > first && b.Old <= last {
			return false
		}
	}
	return true
}

// MapStart maps the first line of an extent: an unchanged line maps exactly;
// a changed one maps to the first line that replaced it, or the line after
// the removal.
func (lm *LineMap) MapStart(old int) int {
	if n, ok := lm.Map(old); ok {
		return n
	}
	b := lm.blockAt(old)
	return b.New
}

// MapEnd maps the last line of an extent: an unchanged line maps exactly; a
// changed one maps to the last line that replaced it, or the line before the
// removal.
func (lm *LineMap) MapEnd(old int) int {
	if n, ok := lm.Map(old); ok {
		return n
	}
	b := lm.blockAt(old)
	if b.Added > 0 {
		return b.New + b.Added - 1
	}
	return b.New - 1
}

func (lm *LineMap) blockAt(old int) block {
	for _, b := range lm.blocks {
		if old >= b.Old && old < b.Old+b.Removed {
			return b
		}
	}
	return block{Old: old, New: old}
}

// HasAdditions reports whether the diff added any line.
func (lm *LineMap) HasAdditions() bool {
	for _, b := range lm.blocks {
		if b.Added > 0 {
			return true
		}
	}
	return false
}
