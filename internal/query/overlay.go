package query

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/overlay"
)

// overlayCheckInterval bounds how often the working tree is diffed against
// the indexed commit: a burst of tool calls shares one check.
const overlayCheckInterval = time.Second

// overlayState is the engine's view of the index overlay: the base index it
// was built from, the working-tree state it reflects, and what it covers.
type overlayState struct {
	mu        sync.Mutex
	base      *scip.SCIPIndex
	key       string
	info      *overlay.Info
	checkedAt time.Time
	err       string
}

// overlayDisabled reports whether CKB_OVERLAY=0 turned the overlay off
// (the replay harness uses it to get the plain base index).
func overlayDisabled() bool {
	v := os.Getenv("CKB_OVERLAY")
	return v == "0" || v == "false" || v == "off"
}

// RefreshOverlay brings the index overlay up to the working tree: when the
// changes since the indexed commit differ from the ones the current overlay
// was built from, it patches the base index again and swaps the result in.
// Cheap when nothing changed (at most one git diff per overlayCheckInterval).
// Returns true when the overlay changed.
func (e *Engine) RefreshOverlay(ctx context.Context) bool {
	if e == nil || e.scipAdapter == nil || overlayDisabled() {
		return false
	}
	base := e.scipAdapter.BaseIndex()
	if base == nil {
		return false
	}
	st := &e.overlay
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.base == base && time.Since(st.checkedAt) < overlayCheckInterval {
		return false
	}
	st.checkedAt = time.Now()

	ch, err := overlay.LoadChanges(ctx, e.repoRoot, base.IndexedCommit)
	if err != nil {
		// Without a known indexed commit there is nothing to diff against;
		// serve the base as before.
		changed := st.base != base || st.info != nil
		st.base, st.key, st.info, st.err = base, "", nil, err.Error()
		e.scipAdapter.SetOverlay(base, nil)
		return changed
	}
	if st.base == base && st.key == ch.Key {
		return false
	}
	idx, info := overlay.Build(base, ch)
	if !e.scipAdapter.SetOverlay(base, idx) {
		return false // the base was reloaded meanwhile; the next call redoes it
	}
	prevHadFiles := st.info != nil && len(st.info.Files) > 0
	st.base, st.key, st.info, st.err = base, ch.Key, info, ""
	if len(info.Files) > 0 || prevHadFiles {
		e.logger.Debug("index overlay rebuilt", "files", len(info.Files), "covered", info.Covered(), "ms", info.BuildMs)
		// Cached answers carry the previous positions.
		if err := e.ClearAllCache(); err != nil {
			e.logger.Warn("Failed to clear caches after overlay rebuild", "error", err.Error())
		}
	}
	return true
}

// OverlayInfo returns what the current overlay covers, or nil when there is
// none (no index, overlay disabled, or no indexed commit to diff against).
func (e *Engine) OverlayInfo() *overlay.Info {
	if e == nil {
		return nil
	}
	e.overlay.mu.Lock()
	defer e.overlay.mu.Unlock()
	if e.scipAdapter == nil || e.overlay.base != e.scipAdapter.BaseIndex() {
		return nil
	}
	return e.overlay.info
}

// OverlayFreshness is the overlay's part of IndexFreshness and getStatus:
// how many files changed since the indexed commit, how many of them the
// overlay serves, and what it could not do.
type OverlayFreshness struct {
	// Files counts the changed files in a language the index covers.
	Files int `json:"files"`
	// Covered counts those the overlay serves (remapped, moved or removed).
	Covered int `json:"covered"`
	// Uncovered lists the rest: new files, which only a reindex adds.
	Uncovered []string `json:"uncovered,omitempty"`
	// Layers lists the overlay layers in use: 0 is the line remap, exact for
	// every unchanged line; declarations and uses on changed lines are missing
	// until a reindex.
	Layers []int `json:"layers,omitempty"`
	// DroppedOccurrences counts the index entries on changed lines the
	// overlay removed and could not replace.
	DroppedOccurrences int `json:"droppedOccurrences"`
	// UnresolvedSites counts uses on changed lines the overlay could not
	// resolve to a declaration (0 until the reference layer exists).
	UnresolvedSites int `json:"unresolvedSites"`
	// BuildMs is how long the last overlay build took.
	BuildMs float64 `json:"buildMs"`
}

// OverlayFreshness summarizes the current overlay, or nil when there is none.
func (e *Engine) OverlayFreshness() *OverlayFreshness {
	info := e.OverlayInfo()
	if info == nil {
		return nil
	}
	f := &OverlayFreshness{Files: len(info.Files), Covered: info.Covered(), Layers: info.Layers(), BuildMs: info.BuildMs}
	for _, fi := range info.Files {
		f.DroppedOccurrences += fi.Dropped
		if fi.State == overlay.StateUncovered {
			f.Uncovered = append(f.Uncovered, fi.Path)
		}
	}
	return f
}

// coveredByOverlay returns the paths the current overlay serves.
func (e *Engine) coveredByOverlay() map[string]bool {
	info := e.OverlayInfo()
	if info == nil {
		return nil
	}
	out := map[string]bool{}
	for _, fi := range info.Files {
		if fi.State != overlay.StateUncovered {
			out[fi.Path] = true
			if fi.OldPath != "" {
				out[fi.OldPath] = true
			}
		}
	}
	return out
}
