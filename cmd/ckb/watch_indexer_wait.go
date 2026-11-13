package main

import (
	"os"
	"os/exec"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// firstIndexRecheck is how often a first index that found no indexer looks
// for one on PATH again: one LookPath per missing indexer, nothing else.
var firstIndexRecheck = 30 * time.Second

// indexerWait holds the indexer binaries a background build is waiting for.
// A build that failed with errIndexerUnavailable used to switch itself off for
// the life of the process, so installing the indexer did nothing until the MCP
// server restarted. Now the next watch tick (or first-index recheck) looks the
// binaries up on PATH and builds as soon as one of them is there.
type indexerWait struct {
	commands []string
	// lookPath is exec.LookPath; tests replace it.
	lookPath func(string) (string, error)
}

// await starts waiting for cmds. False when there is nothing to wait for
// (no known indexer is missing), so the caller can give up as before.
func (w *indexerWait) await(cmds []string) bool {
	w.commands = cmds
	return len(cmds) > 0
}

// waiting reports whether a build is held back until an indexer appears.
func (w *indexerWait) waiting() bool { return len(w.commands) > 0 }

// ready reports whether a build may run: nothing is awaited, or one of the
// awaited indexers is now on PATH, which ends the wait.
func (w *indexerWait) ready() bool {
	if len(w.commands) == 0 {
		return true
	}
	look := w.lookPath
	if look == nil {
		look = exec.LookPath
	}
	for _, c := range w.commands {
		if _, err := look(c); err == nil {
			w.commands = nil
			return true
		}
	}
	return false
}

// missingIndexers lists the indexer binaries not on PATH for the languages a
// background build covers (indexedTargets: project.json's languages, else
// every detected one, manifest-less Python included).
//
// Once an index exists, index.IndexMeta.CheckFreshness reports it stale when a
// skipped language's indexer appears. With no indexer at all no build ran and
// there is no metadata to say so: this is the wait for that case.
func missingIndexers(repoRoot string) []string {
	targets, ok := indexedTargets(repoRoot)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var missing []string
	for _, t := range targets {
		info := project.GetIndexerInfo(t.Lang)
		if info == nil || info.CheckCommand == "" || seen[info.CheckCommand] {
			continue
		}
		seen[info.CheckCommand] = true
		if _, err := exec.LookPath(info.CheckCommand); err != nil {
			missing = append(missing, info.CheckCommand)
		}
	}
	return missing
}

// pathExists reports whether path exists.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
