package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// commitTouching appends a line to each path and commits them together.
func commitTouching(t *testing.T, root, msg string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		full := filepath.Join(root, path)
		content, _ := os.ReadFile(full)
		writeRepoFile(t, root, path, string(content)+"// "+msg+"\n")
	}
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", msg)
}

// coChangeRepo has an editor that moved with its importer in four of five commits,
// and a bystander with a history of its own.
func coChangeRepo(t *testing.T) (*Engine, string) {
	t.Helper()
	engine, root := doneCheckRepo(t)
	for _, path := range []string{"cv/editor.go", "cv/importer.go", "cv/other.go"} {
		writeRepoFile(t, root, path, "package cv\n")
	}
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "cv files")
	for i := 0; i < 4; i++ {
		commitTouching(t, root, "field", "cv/editor.go", "cv/importer.go")
	}
	commitTouching(t, root, "editor only", "cv/editor.go")
	commitTouching(t, root, "other", "cv/other.go")
	commitTouching(t, root, "other again", "cv/other.go")
	return engine, root
}

func TestCoChangeCheck_ListsThePartnerLeftAlone(t *testing.T) {
	engine, root := coChangeRepo(t)
	editRepoFile(t, root, "cv/editor.go", "package cv\n", "package cv\n\ntype CV struct{ Title string }\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).CoChangeCheck
	if check == nil || check.Status != CoChangeOpen {
		t.Fatalf("check = %+v, want open", check)
	}
	if len(check.OpenFiles) != 1 {
		t.Fatalf("open files = %+v, want only the importer", check.OpenFiles)
	}
	got := check.OpenFiles[0]
	if got.File != "cv/importer.go" || got.With != "cv/editor.go" || got.Together != 5 || got.Of != 6 {
		t.Errorf("got %+v, want importer with editor in 5 of 6", got)
	}
	if got.LastTogether == "" || !strings.HasPrefix(got.LastTogether, time.Now().UTC().Format("2006")) {
		t.Errorf("lastTogether = %q, want today's year", got.LastTogether)
	}
	if check.OpenCount != 1 || check.CommitsRead != 6 {
		t.Errorf("counts = %d open, %d read; want 1 and 6", check.OpenCount, check.CommitsRead)
	}
}

func TestCoChangeCheck_PartnerInTheDiffIsNotOpen(t *testing.T) {
	engine, root := coChangeRepo(t)
	editRepoFile(t, root, "cv/editor.go", "package cv\n", "package cv\n\ntype CV struct{ Title string }\n")
	editRepoFile(t, root, "cv/importer.go", "package cv\n", "package cv\n\nfunc Import() CV { return CV{} }\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).CoChangeCheck
	if check == nil || check.Status != CoChangeClear || len(check.OpenFiles) != 0 {
		t.Fatalf("check = %+v, want clear", check)
	}
}

func TestCoChangeCheck_PartnersUnderTheFloorAreClear(t *testing.T) {
	engine, root := coChangeRepo(t)
	editRepoFile(t, root, "cv/other.go", "package cv\n", "package cv\n\nvar x = 1\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).CoChangeCheck
	if check == nil || check.Status != CoChangeClear {
		t.Fatalf("check = %+v, want clear: other.go has 3 commits and moved with the others in one", check)
	}
}

func TestCoChangeCheck_TooLittleHistoryIsNotAssessed(t *testing.T) {
	engine, root := coChangeRepo(t)
	changeTargetSignature(t, root) // lib/lib.go has the base commit only

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).CoChangeCheck
	if check == nil || check.Status != CoChangeNotAssessed {
		t.Fatalf("check = %+v, want not_assessed", check)
	}
}

func TestCoChangeCheck_NothingChangedIsClear(t *testing.T) {
	engine, _ := coChangeRepo(t)
	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).CoChangeCheck
	if check == nil || check.Status != CoChangeClear || check.Note != NothingToAssess {
		t.Fatalf("check = %+v, want clear with nothing to assess", check)
	}
}

func TestCoChangeCheck_NewFileHasNoHistory(t *testing.T) {
	engine, root := coChangeRepo(t)
	writeRepoFile(t, root, "cv/brand_new.go", "package cv\n")
	gitRun(t, root, "add", "cv/brand_new.go") // Untracked would not be in the diff at all
	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).CoChangeCheck
	if check == nil || check.Status != CoChangeNotAssessed {
		t.Fatalf("check = %+v, want not_assessed", check)
	}
}

func TestParseCoChangeLog(t *testing.T) {
	out := []byte("\x01abc 1700000000\n\na.go\nb.go\n\n\x01def 1700000100\n\nc.go\n")
	commits := parseCoChangeLog(out)
	if len(commits) != 2 {
		t.Fatalf("commits = %+v", commits)
	}
	if got := commits[0].files; len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Errorf("first commit files = %v", got)
	}
	if commits[0].when.Unix() != 1700000000 || commits[1].files[0] != "c.go" {
		t.Errorf("commits = %+v", commits)
	}
}

func TestScoreCoChanges(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 1, n, 0, 0, 0, 0, time.UTC) }
	changed := map[string]bool{"editor.go": true}
	commits := []coChangeCommit{
		{day(1), []string{"editor.go", "importer.go", "CHANGELOG.md"}},
		{day(2), []string{"editor.go", "importer.go"}},
		{day(3), []string{"editor.go", "importer.go", "gone.go"}},
		{day(4), []string{"editor.go", "other.go"}},
		{day(5), []string{"other.go", "importer.go"}}, // No changed file: not counted
	}
	sweep := coChangeCommit{when: day(6)}
	for i := 0; i < coChangeMaxFilesPerCommit+1; i++ {
		sweep.files = append(sweep.files, "f"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+".go")
	}
	sweep.files[0] = "editor.go"
	commits = append(commits, sweep)

	check := scoreCoChanges(commits, changed, func(path string) bool { return path != "gone.go" })
	if check.Status != CoChangeOpen || check.CommitsRead != 4 {
		t.Fatalf("check = %+v", check)
	}
	if len(check.OpenFiles) != 1 || check.OpenFiles[0].File != "importer.go" {
		t.Fatalf("open = %+v, want importer only: other.go is under the floor, gone.go doesn't count", check.OpenFiles)
	}
	got := check.OpenFiles[0]
	if got.Together != 3 || got.Of != 4 || got.Share != 0.75 || got.LastTogether != "2026-01-03" {
		t.Errorf("got %+v", got)
	}
}

func TestReadCoChangeHistory_UnknownPathIsEmpty(t *testing.T) {
	_, root := coChangeRepo(t)
	commits, err := readCoChangeHistory(context.Background(), root, []string{"nowhere/none.go"})
	if err != nil || len(commits) != 0 {
		t.Fatalf("commits = %v, err = %v", commits, err)
	}
}
