package overlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// diffOf runs git diff -U0 between two texts and parses it, the same way
// LoadChanges does.
func diffOf(t *testing.T, oldText, newText string) []impact.ChangedHunk {
	t.Helper()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte(oldText), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(newText), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("git", "diff", "--no-index", "-U0", "--no-color", a, b).Output()
	parsed, err := diff.ParseGitDiff(string(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Files) == 0 {
		return nil
	}
	return parsed.Files[0].Hunks
}

// checkMap verifies Map against the texts themselves: every old line Map
// keeps must hold the same text at its new line, and every old line it drops
// must have been removed by the diff. Lines are made unique so a match is
// the line itself.
func checkMap(t *testing.T, oldLines, newLines []string, lm *LineMap) {
	t.Helper()
	newPos := map[string]int{}
	for i, l := range newLines {
		newPos[l] = i + 1
	}
	for i, l := range oldLines {
		n, ok := lm.Map(i + 1)
		p, inNew := newPos[l]
		if ok != inNew {
			t.Errorf("old line %d %q: mapped=%v, still in new file=%v", i+1, l, ok, inNew)
			continue
		}
		if ok && n != p {
			t.Errorf("old line %d %q: mapped to %d, is at %d", i+1, l, n, p)
		}
	}
}

func lines(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
	}
	return out
}

func TestLineMap_AgainstGitDiff(t *testing.T) {
	old := lines("o", 40)
	cases := map[string][]string{
		"insert at top":    append([]string{"n1", "n2"}, old...),
		"insert in middle": append(append(append([]string{}, old[:10]...), "n1", "n2", "n3"), old[10:]...),
		"delete":           append(append([]string{}, old[:5]...), old[9:]...),
		"replace grow":     append(append(append([]string{}, old[:20]...), "n1", "n2", "n3"), old[22:]...),
		"replace shrink":   append(append(append([]string{}, old[:20]...), "n1"), old[25:]...),
		"several": append(append(append(append(append([]string{"n0"}, old[:3]...), old[6:15]...), "n1", "n2"),
			old[15:30]...), old[33:]...),
		"append at end": append(append([]string{}, old...), "n1"),
		"delete last":   old[:39],
		"empty to all":  nil,
	}
	for name, nl := range cases {
		t.Run(name, func(t *testing.T) {
			hunks := diffOf(t, strings.Join(old, "\n")+"\n", strings.Join(nl, "\n")+"\n")
			checkMap(t, old, nl, NewLineMap(hunks))

			// The line-list fallback gives the same map.
			var bare []impact.ChangedHunk
			for _, h := range hunks {
				bare = append(bare, impact.ChangedHunk{Removed: h.Removed, Added: h.Added})
			}
			checkMap(t, old, nl, NewLineMap(bare))
		})
	}
}

func TestLineMap_UnchangedAndExtents(t *testing.T) {
	old := lines("o", 20)
	// Replace lines 5-6 with three lines, insert one line before old 12.
	nl := append(append(append(append(append([]string{}, old[:4]...), "n1", "n2", "n3"), old[6:11]...), "n4"), old[11:]...)
	lm := NewLineMap(diffOf(t, strings.Join(old, "\n")+"\n", strings.Join(nl, "\n")+"\n"))

	if !lm.Unchanged(1, 4) || lm.Unchanged(4, 5) || lm.Unchanged(6, 6) {
		t.Errorf("Unchanged around the replacement is wrong")
	}
	if lm.Unchanged(11, 12) {
		t.Errorf("a line inserted between 11 and 12 must make 11..12 changed")
	}
	if !lm.Unchanged(12, 15) {
		t.Errorf("12..15 is unchanged")
	}
	// An extent starting on the replaced line 5 starts at the first new line.
	if got := lm.MapStart(5); got != 5 {
		t.Errorf("MapStart(5) = %d, want 5", got)
	}
	// One ending on the replaced line 6 ends at the last new line, 7.
	if got := lm.MapEnd(6); got != 7 {
		t.Errorf("MapEnd(6) = %d, want 7", got)
	}
	if got, _ := lm.Map(20); got != 22 {
		t.Errorf("Map(20) = %d, want 22", got)
	}
}
