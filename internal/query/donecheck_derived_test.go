package query

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
)

// The zod fixture: schemas that nest, and the types read off them with `typeof`.
// scip-typescript gives none of these definitions a range, only the name's line.
const zodPkg = "scip-typescript npm m 1.0.0 zod/"

var zodFixtureFiles = map[string]string{
	"zod/schema.ts": `import { z } from "zod";

export const itemSchema = z.object({
  id: z.string(),
});

export const profileSchema = z.object({
  id: z.string(),
  items: z.array(itemSchema),
});

export const projectSchema = z
  .object({
    profile: profileSchema,
  });

export const KINDS = ["a", "b"] as const;

export const parseProfile = (raw: unknown) => profileSchema.parse(raw);

export type Profile = z.infer<typeof profileSchema>;
export type Project = z.infer<typeof projectSchema>;
export type Kind = (typeof KINDS)[number];
export type Parsed = ReturnType<typeof parseProfile>;
`,
	"zod/sample.ts": `import type { Profile, Project, Kind, Parsed } from "./schema";

export const sample: Profile = {
  id: "1",
  items: [],
};

export const project: Project = {
  profile: {
    id: "2",
    items: [],
  },
};

export const LABELS: Record<Kind, string> = { a: "A", b: "B" };

export function show(p: Profile): string {
  return p.id;
}

export const parsed: Parsed = { id: "3", items: [] };
`,
}

// zodOccs finds each named word in the fixture text, so the positions can't drift
// from the files. A definition is the word after const, type or function.
func zodDoc(path string, symbols map[string]string) *scippb.Document {
	var occs []typeOcc
	for i, line := range strings.Split(zodFixtureFiles[path], "\n") {
		for name, symbol := range symbols {
			col := wordColumn(line, name)
			if col < 0 {
				continue
			}
			before := strings.TrimSpace(line[:col])
			def := strings.HasSuffix(before, "const") || strings.HasSuffix(before, "type") || strings.HasSuffix(before, "function")
			occs = append(occs, typeOcc{symbol: symbol, line: int32(i), col: int32(col), def: def}) // #nosec G115 -- fixture
		}
	}
	slices.SortFunc(occs, func(a, b typeOcc) int {
		if a.line != b.line {
			return int(a.line - b.line)
		}
		return int(a.col - b.col)
	})
	return typeFixtureDoc(path, "typescript", occs)
}

func wordColumn(line, name string) int {
	for at := 0; ; {
		i := strings.Index(line[at:], name)
		if i < 0 {
			return -1
		}
		start, end := at+i, at+i+len(name)
		isWord := func(c byte) bool {
			return c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
		}
		if (start == 0 || !isWord(line[start-1])) && (end == len(line) || !isWord(line[end])) {
			return start
		}
		at = start + 1
	}
}

func zodFixtureRepo(t *testing.T) (*Engine, string) {
	t.Helper()
	schema := map[string]string{
		"itemSchema":    zodPkg + "`schema.ts`/itemSchema.",
		"profileSchema": zodPkg + "`schema.ts`/profileSchema.",
		"projectSchema": zodPkg + "`schema.ts`/projectSchema.",
		"KINDS":         zodPkg + "`schema.ts`/KINDS.",
		"parseProfile":  zodPkg + "`schema.ts`/parseProfile.",
		"Profile":       zodPkg + "`schema.ts`/Profile#",
		"Project":       zodPkg + "`schema.ts`/Project#",
		"Kind":          zodPkg + "`schema.ts`/Kind#",
		"Parsed":        zodPkg + "`schema.ts`/Parsed#",
	}
	sample := map[string]string{
		"Profile": schema["Profile"], "Project": schema["Project"], "Kind": schema["Kind"], "Parsed": schema["Parsed"],
		"sample":  zodPkg + "`sample.ts`/sample.",
		"project": zodPkg + "`sample.ts`/project.",
		"LABELS":  zodPkg + "`sample.ts`/LABELS.",
		"show":    zodPkg + "`sample.ts`/show().",
		"parsed":  zodPkg + "`sample.ts`/parsed.",
	}
	engine := changeSetEngine(t, zodDoc("zod/schema.ts", schema), zodDoc("zod/sample.ts", sample))
	root := engine.repoRoot
	writeRepoFile(t, root, ".gitignore", ".ckb/\nindex.scip\n")
	for path, content := range doneCheckFiles {
		writeRepoFile(t, root, path, content)
	}
	for path, content := range zodFixtureFiles {
		writeRepoFile(t, root, path, content)
	}
	gitInit(t, root)
	engine.scipAdapter.GetIndex().IndexedCommit = headCommit(t, root)
	return engine, root
}

// A key added inside z.object changes the type read off the schema with typeof, and
// the type of every schema that holds it. The literals of those types are what breaks:
// the index has no edge from the key to them.
func TestDoneCheck_TSTypeofCarriesASchemaChangeToItsType(t *testing.T) {
	engine, root := zodFixtureRepo(t)
	editRepoFile(t, root, "zod/schema.ts", "  items: z.array(itemSchema),\n", "  items: z.array(itemSchema),\n  custom: z.array(z.string()).default([]),\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	// The Profile literal and the `profile:` key in the Project literal. Not
	// `show(p: Profile)`, and not what a function using the schema returns: following
	// every function that mentions a schema would follow the call graph.
	want := "[zod/sample.ts:3 zod/sample.ts:9]"
	if got := openSiteKeys(check); check.Status != DoneCheckOpen || fmt.Sprint(got) != want {
		t.Fatalf("status %s, open sites = %v, want %s", check.Status, got, want)
	}
	for _, site := range check.OpenSites {
		if !strings.Contains(site.Via, "typeof") {
			t.Errorf("site %s:%d via = %q, want it to say the type comes from typeof", site.File, site.Line, site.Via)
		}
	}
	if site := check.OpenSites[1]; site.Symbol != "Project" || !strings.Contains(site.Code, "profile:") {
		t.Errorf("nested site = %+v, want Project at its profile key", site)
	}
}

// A schema two levels down reaches the types above it at the key that holds it.
func TestDoneCheck_TSTypeofFollowsNestedSchemas(t *testing.T) {
	engine, root := zodFixtureRepo(t)
	editRepoFile(t, root, "zod/schema.ts", "export const itemSchema = z.object({\n  id: z.string(),\n", "export const itemSchema = z.object({\n  id: z.string(),\n  note: z.string(),\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	want := "[zod/sample.ts:5 zod/sample.ts:9]"
	if got := openSiteKeys(check); fmt.Sprint(got) != want {
		t.Fatalf("open sites = %v, want %s: Profile at items, Project at profile", got, want)
	}
}

// A union read off an `as const` array breaks the Record keyed by it.
func TestDoneCheck_TSTypeofArrayBreaksTheRecordKeyedByIt(t *testing.T) {
	engine, root := zodFixtureRepo(t)
	editRepoFile(t, root, "zod/schema.ts", `["a", "b"] as const`, `["a", "b", "c"] as const`)

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if got := openSiteKeys(check); fmt.Sprint(got) != "[zod/sample.ts:15]" {
		t.Fatalf("open sites = %v, want the Record literal", got)
	}
}

// A literal the diff already touched is handled, like any other use.
func TestDoneCheck_TSTypeofLiteralTouchedIsUpdated(t *testing.T) {
	engine, root := zodFixtureRepo(t)
	editRepoFile(t, root, "zod/schema.ts", "  items: z.array(itemSchema),\n", "  items: z.array(itemSchema),\n  custom: z.array(z.string()),\n")
	editRepoFile(t, root, "zod/sample.ts", "  id: \"1\",\n  items: [],\n", "  id: \"1\",\n  items: [],\n  custom: [],\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if got := openSiteKeys(check); fmt.Sprint(got) != "[zod/sample.ts:10]" {
		t.Fatalf("open sites = %v, want the Project literal left as it was", got)
	}
}

// ReturnType<typeof fn> changes with the function it is read off.
func TestDoneCheck_TSTypeofFunctionChangesItsReturnType(t *testing.T) {
	engine, root := zodFixtureRepo(t)
	editRepoFile(t, root, "zod/schema.ts", "profileSchema.parse(raw);", "profileSchema.safeParse(raw);")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if got := openSiteKeys(check); fmt.Sprint(got) != "[zod/sample.ts:21]" {
		t.Fatalf("open sites = %v, want the literal typed as the function's return", got)
	}
}

// A schema the diff edited that also holds an edited schema: its literal is listed
// once, at the key, not a second time at its first line.
func TestDoneCheck_TSTypeofListsALiteralOnceAtItsKey(t *testing.T) {
	engine, root := zodFixtureRepo(t)
	editRepoFile(t, root, "zod/schema.ts", "  items: z.array(itemSchema),\n", "  items: z.array(itemSchema),\n  custom: z.array(z.string()).default([]),\n")
	editRepoFile(t, root, "zod/schema.ts", "    profile: profileSchema,\n  });", "    profile: profileSchema,\n  })\n  .strict();")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if got := openSiteKeys(check); fmt.Sprint(got) != "[zod/sample.ts:3 zod/sample.ts:9]" {
		t.Fatalf("open sites = %v, want the Profile literal and Project at its profile key", got)
	}
}
