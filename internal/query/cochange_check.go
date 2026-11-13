package query

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
)

// Co-change check statuses, with the done check's meaning: open is a file left alone,
// clear is none found where the history could have shown one.
const (
	CoChangeOpen        = "open"
	CoChangeClear       = "clear"
	CoChangeNotAssessed = "not_assessed"
)

const (
	// coChangeMaxCommits bounds the commits of the changed files that are read, newest
	// first. Older habits are not this diff's.
	coChangeMaxCommits = 400
	// coChangeMaxFilesPerCommit: a commit touching more files than this (a formatter
	// run, a rename sweep) says nothing about which files belong together.
	coChangeMaxFilesPerCommit = 50
	// coChangeMinCommits is the commits a changed file needs before its partners mean
	// anything, and coChangeMinTogether the shared commits a partner needs.
	coChangeMinCommits  = 3
	coChangeMinTogether = 3
	// coChangeMinShare is the share of a changed file's commits a partner has to be in.
	coChangeMinShare = 0.5
	// MaxCoChangeFiles caps the files listed; CoChangeCheck.OpenCount stays the real count.
	MaxCoChangeFiles = 20
)

// CoChangeCheck answers a question the index can't: which files usually change with
// the files this diff touched, and were left alone this time? It reads git history
// and nothing else, so it works for every language and for files that aren't code,
// and it finds the importer that moves with the editor whether or not a type ties
// them together. A file listed here may need nothing; the history only says it
// usually did.
type CoChangeCheck struct {
	Status string `json:"status"`
	// OpenFiles are the partners the diff left alone, strongest pattern first.
	OpenFiles []CoChangeFile `json:"openFiles,omitempty"`
	OpenCount int            `json:"openCount"`
	// CommitsRead is how many commits of the changed files were counted.
	CommitsRead int    `json:"commitsRead"`
	Truncated   bool   `json:"truncated,omitempty"`
	Note        string `json:"note,omitempty"`
	// LeftOut counts the partners over the floor that sit in generated or data
	// paths (.ckb/config.json paths) and so aren't listed.
	LeftOut *pathclass.Tally `json:"leftOut,omitempty"`
}

// CoChangeFile is one file that usually changes with a changed file and didn't.
type CoChangeFile struct {
	File string `json:"file"`
	// With is the changed file it moves with most.
	With string `json:"with"`
	// Together is the commits that touched both; Of is the commits that touched With.
	Together int     `json:"together"`
	Of       int     `json:"of"`
	Share    float64 `json:"share"`
	// LastTogether is the day of the newest commit that touched both.
	LastTogether string `json:"lastTogether,omitempty"`
}

// coChangeCommit is one commit of the history read: when, and what it touched.
type coChangeCommit struct {
	when  time.Time
	files []string
}

// coChangeCheck runs the check for the files of a parsed diff.
func (e *Engine) coChangeCheck(ctx context.Context, parsed *impact.ParsedDiff) *CoChangeCheck {
	if len(parsed.Files) == 0 {
		return &CoChangeCheck{Status: CoChangeClear, Note: NothingToAssess}
	}
	changed := make(map[string]bool, len(parsed.Files))
	var paths []string
	for i := range parsed.Files {
		file := &parsed.Files[i]
		for _, path := range []string{diff.GetEffectivePath(file), file.OldPath} {
			if path != "" && !changed[path] {
				changed[path] = true
				paths = append(paths, path)
			}
		}
	}
	commits, err := readCoChangeHistory(ctx, e.repoRoot, paths)
	if err != nil {
		return &CoChangeCheck{Status: CoChangeNotAssessed, Note: "The history of the changed files couldn't be read: " + err.Error() + "."}
	}
	classes := pathclass.FromConfig(e.repoRoot, e.config)
	var leftOut pathclass.Counter
	check := scoreCoChanges(commits, changed, func(path string) bool {
		if leftOut.Skip(classes, path) || !diff.IsSourceFile(path) {
			return false
		}
		_, err := os.Stat(filepath.Join(e.repoRoot, path))
		return err == nil
	})
	if check.LeftOut = leftOut.Tally(); check.LeftOut != nil {
		check.Note += " " + check.LeftOut.Note + "."
	}
	return check
}

// readCoChangeHistory returns the newest commits that touched any of the paths, each
// with every file it touched. Two git calls: the log of the paths only knows the
// paths, so the commits are listed again without a pathspec.
func readCoChangeHistory(ctx context.Context, repoRoot string, paths []string) ([]coChangeCommit, error) {
	args := append([]string{"log", "--no-merges", "--format=%H", "-n", strconv.Itoa(coChangeMaxCommits), "--"}, paths...)
	out, err := gitOutput(ctx, repoRoot, args...)
	if err != nil {
		return nil, err
	}
	hashes := strings.Fields(string(out))
	if len(hashes) == 0 {
		return nil, nil
	}
	args = append([]string{"log", "--no-walk=unsorted", "--name-only", "--format=%x01%H %ct"}, hashes...)
	out, err = gitOutput(ctx, repoRoot, args...)
	if err != nil {
		return nil, err
	}
	return parseCoChangeLog(out), nil
}

func gitOutput(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- fixed binary, no shell; args are git subcommands and hashes from git itself
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out, nil
}

// parseCoChangeLog reads `git log --name-only --format=%x01%H %ct`: a header line per
// commit, then its files, blank lines between.
func parseCoChangeLog(out []byte) []coChangeCommit {
	var commits []coChangeCommit
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "\x01") {
			commit := coChangeCommit{}
			if fields := strings.Fields(line[1:]); len(fields) == 2 {
				if secs, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					commit.when = time.Unix(secs, 0).UTC()
				}
			}
			commits = append(commits, commit)
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || len(commits) == 0 {
			continue
		}
		commits[len(commits)-1].files = append(commits[len(commits)-1].files, line)
	}
	return commits
}

// coChangePair counts one partner against one changed file.
type coChangePair struct {
	together int
	last     time.Time
}

// scoreCoChanges counts, per changed file, how often each other file moved with it,
// and lists the partners over the floor that the diff left alone. counts says
// whether a partner is worth listing at all.
func scoreCoChanges(commits []coChangeCommit, changed map[string]bool, counts func(string) bool) *CoChangeCheck {
	commitsOf := make(map[string]int)
	pairs := make(map[string]map[string]*coChangePair) // partner -> changed file -> counts
	read := 0
	for _, commit := range commits {
		if len(commit.files) > coChangeMaxFilesPerCommit {
			continue
		}
		// A commit touching one changed file alone counts: it is evidence that the file
		// moves on its own, and lowers every partner's share.
		var anchors, partners []string
		for _, file := range commit.files {
			if changed[file] {
				anchors = append(anchors, file)
			} else {
				partners = append(partners, file)
			}
		}
		if len(anchors) == 0 {
			continue
		}
		read++
		for _, anchor := range anchors {
			commitsOf[anchor]++
		}
		for _, partner := range partners {
			if pairs[partner] == nil {
				pairs[partner] = make(map[string]*coChangePair)
			}
			for _, anchor := range anchors {
				pair := pairs[partner][anchor]
				if pair == nil {
					pair = &coChangePair{}
					pairs[partner][anchor] = pair
				}
				pair.together++
				if commit.when.After(pair.last) {
					pair.last = commit.when
				}
			}
		}
	}

	check := &CoChangeCheck{CommitsRead: read}
	enough := 0
	for _, n := range commitsOf {
		if n >= coChangeMinCommits {
			enough++
		}
	}

	for partner, byAnchor := range pairs {
		var best *CoChangeFile
		for anchor, pair := range byAnchor {
			of := commitsOf[anchor]
			if of < coChangeMinCommits || pair.together < coChangeMinTogether {
				continue
			}
			share := float64(pair.together) / float64(of)
			if share < coChangeMinShare {
				continue
			}
			candidate := &CoChangeFile{File: partner, With: anchor, Together: pair.together, Of: of, Share: share}
			if !pair.last.IsZero() {
				candidate.LastTogether = pair.last.Format("2006-01-02")
			}
			if best == nil || candidate.Share > best.Share || (candidate.Share == best.Share && candidate.Together > best.Together) ||
				(candidate.Share == best.Share && candidate.Together == best.Together && candidate.With < best.With) {
				best = candidate
			}
		}
		if best != nil && counts(partner) {
			check.OpenFiles = append(check.OpenFiles, *best)
		}
	}
	sort.Slice(check.OpenFiles, func(i, j int) bool {
		a, b := &check.OpenFiles[i], &check.OpenFiles[j]
		if a.Share != b.Share {
			return a.Share > b.Share
		}
		if a.Together != b.Together {
			return a.Together > b.Together
		}
		return a.File < b.File
	})
	check.OpenCount = len(check.OpenFiles)
	if len(check.OpenFiles) > MaxCoChangeFiles {
		check.OpenFiles = check.OpenFiles[:MaxCoChangeFiles]
		check.Truncated = true
	}

	switch {
	case len(check.OpenFiles) > 0:
		check.Status = CoChangeOpen
		check.Note = "Files that changed with a touched file in at least half of its recent commits and were left alone. This is history, not code: some may need nothing."
	case read == 0:
		check.Status = CoChangeNotAssessed
		check.Note = "The changed files have no commits yet, so there is no pattern to compare with."
	case enough == 0:
		check.Status = CoChangeNotAssessed
		check.Note = fmt.Sprintf("No changed file has %d commits yet, so there is no pattern to compare with.", coChangeMinCommits)
	default:
		check.Status = CoChangeClear
		check.Note = fmt.Sprintf("No file usually changes with the touched files and was left alone, in their last %d commits.", read)
	}
	return check
}
