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
const (
	zodPkg       = "scip-typescript npm m 1.0.0 zod/"
	zodProfileID = zodPkg + "`schema.ts`/id1:"
)

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

export const levelSchema = z.enum(["low", "high"]);
export type Level = z.infer<typeof levelSchema>;
`,
	"zod/sample.ts": `import type { Profile, Project, Kind, Parsed } from "./schema";

export const sample: Profile = {
  id: "1",
  items: [{ id: "i" }],
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

export const LEVELS: Record<Level, number> = { low: 0, high: 1 };
export const cast = { items: [] } as Profile;
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
		"levelSchema":   zodPkg + "`schema.ts`/levelSchema.",
		"Level":         zodPkg + "`schema.ts`/Level#",
	}
	sample := map[string]string{
		"Profile": schema["Profile"], "Project": schema["Project"], "Kind": schema["Kind"], "Parsed": schema["Parsed"], "Level": schema["Level"],
		"sample":  zodPkg + "`sample.ts`/sample.",
		"project": zodPkg + "`sample.ts`/project.",
		"LABELS":  zodPkg + "`sample.ts`/LABELS.",
		"show":    zodPkg + "`sample.ts`/show().",
		"parsed":  zodPkg + "`sample.ts`/parsed.",
		"LEVELS":  zodPkg + "`sample.ts`/LEVELS.",
		"cast":    zodPkg + "`sample.ts`/cast.",
	}
	// The key `id` of profileSchema, as scip-typescript has it: defined where the schema
	// writes it, referenced where a literal sets it and where it is read.
	schemaDoc, sampleDoc := zodDoc("zod/schema.ts", schema), zodDoc("zod/sample.ts", sample)
	schemaDoc.Occurrences = append(schemaDoc.Occurrences, &scippb.Occurrence{Symbol: zodProfileID, Range: []int32{7, 2, 4}, SymbolRoles: int32(scippb.SymbolRole_Definition)})
	for _, at := range [][]int32{{3, 2, 4}, {9, 4, 6}, {17, 11, 13}} {
		sampleDoc.Occurrences = append(sampleDoc.Occurrences, &scippb.Occurrence{Symbol: zodProfileID, Range: at})
	}
	engine := changeSetEngine(t, schemaDoc, sampleDoc)
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
	if !strings.Contains(check.OpenSites[0].Via, "now requires custom") {
		t.Errorf("via = %q, want it to name the key", check.OpenSites[0].Via)
	}
}

// A schema two levels down reaches the types above it at the key that holds it.
func TestDoneCheck_TSTypeofFollowsNestedSchemas(t *testing.T) {
	engine, root := zodFixtureRepo(t)
	editRepoFile(t, root, "zod/schema.ts", "export const itemSchema = z.object({\n  id: z.string(),\n", "export const itemSchema = z.object({\n  id: z.string(),\n  note: z.string(),\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	// The item literal in the sample's items. The project's items are an empty array:
	// nothing there lacks the key.
	if got := openSiteKeys(check); fmt.Sprint(got) != "[zod/sample.ts:5]" {
		t.Fatalf("open sites = %v, want the item literal under Profile's items", got)
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
	editRepoFile(t, root, "zod/sample.ts", "  id: \"1\",\n  items: [{ id: \"i\" }],\n", "  id: \"1\",\n  items: [{ id: \"i\" }],\n  custom: [],\n")

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

func shapeContracts(resp *AnalyzeChangeSetResponse) []string {
	var lines []string
	for _, c := range resp.Contracts {
		if c.Kind == "shape-changed" {
			lines = append(lines, c.Symbol+": "+c.Detail)
		}
	}
	return lines
}

// What zod says about a key decides what breaks. `.default(…)` is required in the output
// type; `.optional()` is not, and opens nothing.
func TestDoneCheck_ZodSaysWhatChanged(t *testing.T) {
	t.Run("a key with a default", func(t *testing.T) {
		engine, root := zodFixtureRepo(t)
		editRepoFile(t, root, "zod/schema.ts", "  items: z.array(itemSchema),\n", "  items: z.array(itemSchema),\n  custom: z.array(z.string()).default([]),\n")
		resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})
		if got := shapeContracts(resp); fmt.Sprint(got) != "[Profile: + custom (required in output, default [])]" {
			t.Errorf("contracts = %v", got)
		}
	})
	t.Run("an optional key is clear", func(t *testing.T) {
		engine, root := zodFixtureRepo(t)
		editRepoFile(t, root, "zod/schema.ts", "  items: z.array(itemSchema),\n", "  items: z.array(itemSchema),\n  note: z.string().optional(),\n")
		resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})
		if resp.DoneCheck.Status != DoneCheckClear || resp.DoneCheck.OpenCount != 0 {
			t.Errorf("check = %s with %v, want clear: an optional key breaks no literal", resp.DoneCheck.Status, openSiteKeys(resp.DoneCheck))
		}
		if got := shapeContracts(resp); fmt.Sprint(got) != "[Profile: + note (optional)]" {
			t.Errorf("contracts = %v", got)
		}
	})
	t.Run("only a validator changed", func(t *testing.T) {
		engine, root := zodFixtureRepo(t)
		editRepoFile(t, root, "zod/schema.ts", "  items: z.array(itemSchema),\n", "  items: z.array(itemSchema).max(3),\n")
		resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})
		if resp.DoneCheck.OpenCount != 0 || len(shapeContracts(resp)) != 0 {
			t.Errorf("open %v contracts %v, want neither: the type is the same", openSiteKeys(resp.DoneCheck), shapeContracts(resp))
		}
	})
	t.Run("a removed key opens every place that names it", func(t *testing.T) {
		engine, root := zodFixtureRepo(t)
		editRepoFile(t, root, "zod/schema.ts", "export const profileSchema = z.object({\n  id: z.string(),\n", "export const profileSchema = z.object({\n")
		resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})
		if got := openSiteKeys(resp.DoneCheck); fmt.Sprint(got) != "[zod/sample.ts:4 zod/sample.ts:10 zod/sample.ts:18]" {
			t.Errorf("open sites = %v, want the two literals that set id and the read of it", got)
		}
		if site := resp.DoneCheck.OpenSites[0]; site.Symbol != "profileSchema.id" || site.Change != "removed" {
			t.Errorf("site = %+v, want profileSchema.id removed", site)
		}
		if got := shapeContracts(resp); fmt.Sprint(got) != "[Profile: - id]" {
			t.Errorf("contracts = %v", got)
		}
	})
	t.Run("a wider key opens where it is read", func(t *testing.T) {
		engine, root := zodFixtureRepo(t)
		editRepoFile(t, root, "zod/schema.ts", "export const profileSchema = z.object({\n  id: z.string(),\n", "export const profileSchema = z.object({\n  id: z.union([z.string(), z.number()]),\n")
		resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})
		check := resp.DoneCheck
		if got := rarelySiteKeys(check); fmt.Sprint(got) != "[zod/sample.ts:18]" || len(check.OpenSites) != 0 {
			t.Errorf("open sites = %v, rarely %v, want only `p.id`, as a read that rarely breaks: what was written still fits", openSiteKeys(check), got)
		}
		if check.Status != DoneCheckOpen || len(check.Rarely) != 1 || check.Rarely[0].Symbol != "profileSchema.id" {
			t.Errorf("status %s, rarely %+v: a read left as it was keeps the check open, counted under its key", check.Status, check.Rarely)
		}
	})
	t.Run("a chain that can't be read falls back and says so", func(t *testing.T) {
		engine, root := zodFixtureRepo(t)
		editRepoFile(t, root, "zod/schema.ts", "  items: z.array(itemSchema),\n});", "  items: z.array(itemSchema),\n  note: z.string().optional(),\n}).transform((p) => p);")
		resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})
		got := openSiteKeys(resp.DoneCheck)
		if fmt.Sprint(got) != "[zod/sample.ts:3 zod/sample.ts:9]" || fmt.Sprint(rarelySiteKeys(resp.DoneCheck)) != "[zod/sample.ts:24]" {
			t.Fatalf("open sites = %v, rarely %v, want every literal, and the cast as rarely breaking, as without the reader", got, rarelySiteKeys(resp.DoneCheck))
		}
		if via := resp.DoneCheck.OpenSites[0].Via; !strings.Contains(via, ".transform") {
			t.Errorf("via = %q, want it to say what wasn't read", via)
		}
	})
	t.Run("an enum member added breaks the record keyed by it", func(t *testing.T) {
		engine, root := zodFixtureRepo(t)
		editRepoFile(t, root, "zod/schema.ts", `z.enum(["low", "high"])`, `z.enum(["low", "mid", "high"])`)
		resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})
		if got := openSiteKeys(resp.DoneCheck); fmt.Sprint(got) != "[zod/sample.ts:23]" {
			t.Errorf("open sites = %v, want the Record", got)
		}
		if got := shapeContracts(resp); fmt.Sprint(got) != `[Level: + "mid"]` {
			t.Errorf("contracts = %v", got)
		}
	})
}
