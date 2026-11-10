package query

import (
	"fmt"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
)

// typeFixture is a changed-file fixture around one type: the Go struct CV and the
// TypeScript interface CV, each built in an importer and elsewhere.
const (
	symGoCV       = "scip-go gomod example.com/m v1 `example.com/m/cv`/CV#"
	symGoImport   = "scip-go gomod example.com/m v1 `example.com/m/cv`/Import()."
	symGoBuild    = "scip-go gomod example.com/m v1 `example.com/m/cv`/Build()."
	symTSCV       = "scip-typescript npm m 1.0.0 cv/`types.ts`/CV#"
	symTSImportCv = "scip-typescript npm m 1.0.0 cv/`importer.ts`/importCv()."
)

var typeFixtureFiles = map[string]string{
	"cv/cv.go":       "package cv\n\n// CV is a résumé.\ntype CV struct {\n\tTitle string\n}\n",
	"cv/importer.go": "package cv\n\nfunc Import(title string) CV {\n\treturn CV{\n\t\tTitle: title,\n\t}\n}\n",
	"cv/other.go":    "package cv\n\nvar Empty = CV{}\n\nfunc Build() *CV {\n\treturn &CV{Title: \"x\"}\n}\n",
	"cv/types.ts":    "export interface CV {\n  title: string;\n}\n",
	"cv/importer.ts": "import { CV } from './types';\n\nexport function importCv(raw: any): CV {\n  return {\n    title: raw.title,\n  };\n}\n\nexport const empty: CV = {\n  title: '',\n};\n",
}

// typeOcc is an occurrence at a 0-based line and column.
type typeOcc struct {
	symbol    string
	line, col int32
	def       bool
	kind      scippb.SymbolInformation_Kind
}

func typeFixtureDoc(path, lang string, occs []typeOcc) *scippb.Document {
	doc := &scippb.Document{RelativePath: path, Language: lang}
	seen := make(map[string]bool)
	for _, occ := range occs {
		if occ.def && !seen[occ.symbol] {
			seen[occ.symbol] = true
			doc.Symbols = append(doc.Symbols, &scippb.SymbolInformation{Symbol: occ.symbol, Kind: occ.kind})
		}
		o := &scippb.Occurrence{Symbol: occ.symbol, Range: []int32{occ.line, occ.col, occ.col + 2}}
		if occ.def {
			o.SymbolRoles = int32(scippb.SymbolRole_Definition)
		}
		doc.Occurrences = append(doc.Occurrences, o)
	}
	return doc
}

func typeFixtureRepo(t *testing.T) (*Engine, string) {
	t.Helper()
	engine := changeSetEngine(t,
		typeFixtureDoc("cv/cv.go", "go", []typeOcc{{symGoCV, 3, 5, true, scippb.SymbolInformation_Struct}}),
		typeFixtureDoc("cv/importer.go", "go", []typeOcc{
			{symGoImport, 2, 5, true, scippb.SymbolInformation_Function},
			{symGoCV, 2, 26, false, 0}, // return type
			{symGoCV, 3, 8, false, 0},  // return CV{
		}),
		typeFixtureDoc("cv/other.go", "go", []typeOcc{
			{symGoCV, 2, 12, false, 0}, // CV{}
			{symGoBuild, 4, 5, true, scippb.SymbolInformation_Function},
			{symGoCV, 4, 14, false, 0}, // *CV {
			{symGoCV, 5, 9, false, 0},  // &CV{
		}),
		typeFixtureDoc("cv/types.ts", "typescript", []typeOcc{{symTSCV, 0, 17, true, scippb.SymbolInformation_Interface}}),
		typeFixtureDoc("cv/importer.ts", "typescript", []typeOcc{
			{symTSCV, 0, 9, false, 0}, // import
			{symTSImportCv, 2, 16, true, scippb.SymbolInformation_Function},
			{symTSCV, 2, 36, false, 0}, // ): CV {
			{symTSCV, 8, 20, false, 0}, // : CV = {
		}),
	)
	root := engine.repoRoot
	writeRepoFile(t, root, ".gitignore", ".ckb/\nindex.scip\n")
	for path, content := range doneCheckFiles {
		writeRepoFile(t, root, path, content)
	}
	for path, content := range typeFixtureFiles {
		writeRepoFile(t, root, path, content)
	}
	gitInit(t, root)
	engine.scipAdapter.GetIndex().IndexedCommit = headCommit(t, root)
	return engine, root
}

func openSiteKeys(check *DoneCheck) []string {
	var keys []string
	for _, site := range check.OpenSites {
		keys = append(keys, fmt.Sprintf("%s:%d", site.File, site.Line))
	}
	return keys
}

func TestDoneCheck_GoFieldAddedListsTheLiterals(t *testing.T) {
	engine, root := typeFixtureRepo(t)
	editRepoFile(t, root, "cv/cv.go", "\tTitle string\n", "\tTitle string\n\tName  string\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if check.Status != DoneCheckOpen {
		t.Fatalf("check = %+v, want open", check)
	}
	want := []string{"cv/importer.go:4", "cv/other.go:6"}
	if got := openSiteKeys(check); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("open sites = %v, want %v: the return type, the receiver and CV{} are not literals", got, want)
	}
	for _, site := range check.OpenSites {
		if site.Via != "fields of CV changed" || site.Change != "changed" || site.Symbol != "CV" {
			t.Errorf("site = %+v", site)
		}
	}
}

func TestDoneCheck_GoLiteralTouchedIsUpdated(t *testing.T) {
	engine, root := typeFixtureRepo(t)
	editRepoFile(t, root, "cv/cv.go", "\tTitle string\n", "\tTitle string\n\tName  string\n")
	editRepoFile(t, root, "cv/importer.go", "\t\tTitle: title,\n", "\t\tTitle: title,\n\t\tName:  title,\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if got := openSiteKeys(check); fmt.Sprint(got) != "[cv/other.go:6]" {
		t.Errorf("open sites = %v, want only other.go", got)
	}
	if check.UpdatedCount != 1 {
		t.Errorf("updated = %d, want the importer's literal", check.UpdatedCount)
	}
}

func TestDoneCheck_GoTagChangeIsNotAFieldChange(t *testing.T) {
	engine, root := typeFixtureRepo(t)
	editRepoFile(t, root, "cv/cv.go", "\tTitle string\n", "\tTitle string `json:\"title\"`\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if check.Status != DoneCheckClear {
		t.Errorf("check = %+v, want clear", check)
	}
}

func TestDoneCheck_TSFieldAddedListsTheConstructions(t *testing.T) {
	engine, root := typeFixtureRepo(t)
	editRepoFile(t, root, "cv/types.ts", "  title: string;\n", "  title: string;\n  name: string;\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if check.Status != DoneCheckOpen {
		t.Fatalf("check = %+v, want open", check)
	}
	want := []string{"cv/importer.ts:3", "cv/importer.ts:9"}
	if got := openSiteKeys(check); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("open sites = %v, want %v: the import is not a construction", got, want)
	}

	// Touching the function that returns the type counts as handling it.
	editRepoFile(t, root, "cv/importer.ts", "    title: raw.title,\n", "    title: raw.title,\n    name: raw.name,\n")
	check = runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if got := openSiteKeys(check); fmt.Sprint(got) != "[cv/importer.ts:10]" {
		t.Errorf("open sites = %v, want only the constant, one line further down", got)
	}
}

func TestDoneCheck_TSFieldRemovedListsTheConstructions(t *testing.T) {
	engine, root := typeFixtureRepo(t)
	editRepoFile(t, root, "cv/types.ts", "  title: string;\n", "  headline: string;\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if check.Status != DoneCheckOpen || check.OpenCount != 2 {
		t.Fatalf("check = %+v, want 2 open", check)
	}
}
