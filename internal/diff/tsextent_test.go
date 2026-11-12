package diff

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

func TestTSValueEnd(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int // Last line of the value on line 1; 0 for none
	}{
		{"object", "export const table = {\n  a: 1,\n};\n\nexport const y = 2;", 3},
		{"call with an object", "export const profileSchema = z.object({\n  id: z.string(),\n  custom: z.array(x).default([]),\n});\nexport type P = z.infer<typeof profileSchema>;", 4},
		{"one line", "const x = 1;\nconst y = 2;", 1},
		{"no semicolon", "const x = 1\nconst y = 2", 1},
		{"array as const", "export const KEYS = [\n  \"a\",\n  \"b\",\n] as const;\nconst y = 2;", 4},
		{"method chain on the next line", "export const projectSchema = z\n  .object({\n    a: z.string(),\n  })\n  .superRefine((v, ctx) => {\n    check(v);\n  });\nexport const y = 2;", 7},
		{"satisfies on its own line", "export const SUB = {\n  a: \"b\",\n} as const satisfies Record<\n  string,\n  string\n>;\nconst y = 2;", 6},
		{"typed", "export const m: Record<string, () => void> = {\n  a: () => {},\n};\nconst y = 2;", 3},
		{"template literal", "const q = `select {\n  ${cols.map((c) => `\"${c}\"`).join(\",\")}\n from t`;\nconst y = 2;", 3},
		{"brace in a string", "const x = \"{\";\nconst y = 2;", 1},
		{"apostrophe in a line comment", "const x = [\n  1, // don't\n  2,\n];\nconst y = 2;", 4},
		{"regex with a quote and a brace", "const re = /[\"'{]/g;\nconst y = 2;", 1},
		{"ternary over lines", "const x = a\n  ? b\n  : c;\nconst y = 2;", 3},
		{"binary operator at the end of the line", "const x = a ||\n  b;\nconst y = 2;", 2},
		{"arrow body on the next line", "const f = (a: number) =>\n  a + 1;\nconst y = 2;", 2},
		{"comparison in the value", "const ok = a === b;\nconst y = 2;", 1},
		{"no initializer", "declare const x: number;\nconst y = 2;", 0},
		{"let", "export let state = {\n  a: 1,\n};", 3},
		{"not a variable statement", "export function f() {\n  return 1;\n}", 0},
		{"unclosed", "const x = [\n  1,", 0},
		{"ran into the next statement", "const x = a +\nexport const y = 2;", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := newTSSource(linesOf(tc.src)).valueEnd(1); got != tc.want {
				t.Errorf("valueEnd = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("a line the side doesn't show", func(t *testing.T) {
		lines := linesOf("const table = {\n  a: 1,\n};")
		delete(lines, 2)
		if got := newTSSource(lines).valueEnd(1); got != 0 {
			t.Errorf("valueEnd = %d, want 0: the gap could hold anything", got)
		}
	})
}

func TestIsTSModuleValue(t *testing.T) {
	for symbol, want := range map[string]bool{
		tsPkg + "profileSchema.": true,
		tsPkg + "Profile#":       false,
		tsPkg + "Shape#area().":  false,
		tsPkg + "Shape#name.":    false,
		tsPkg + "run().":         false,
		tsPkg + "run().(x)":      false,
		tsPkg + "name0:":         false,
		tsPkg:                    false,
		goPkg + "Table.":         false,
		"local 3":                false,
		"scip-typescript npm demo 1.0.0 src/`a.b.ts`/x.": true,
	} {
		if got := isTSModuleValue(symbol); got != want {
			t.Errorf("isTSModuleValue(%q) = %v, want %v", symbol, got, want)
		}
	}
}

// zodDoc is src/lib.ts, as scip-typescript indexes it: a const has only the line of
// its name, an object key is a meta symbol, a type alias has only its line.
//
//	1 import { z } from "zod";
//	2
//	3 export const profileSchema = z.object({
//	4   id: z.string(),
//	5   name: z.string(),
//	6 });
//	7
//	8 export type Profile = z.infer<typeof profileSchema>;
func zodDoc() *scip.Document {
	return &scip.Document{
		RelativePath: "src/lib.ts",
		Occurrences: []*scip.Occurrence{
			def(tsPkg, 0, 0, 0, 8, 0),
			def(tsPkg+"profileSchema.", 2),
			def(tsPkg+"id0:", 3),
			def(tsPkg+"name0:", 4),
			def(tsPkg+"Profile#", 7),
			ref(tsPkg+"profileSchema.", 7),
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: tsPkg}, {Symbol: tsPkg + "profileSchema."}, {Symbol: tsPkg + "id0:"},
			{Symbol: tsPkg + "name0:"}, {Symbol: tsPkg + "Profile#"},
		},
	}
}

// scip-typescript gives a const no range, only the line of its name. A key added
// inside `z.object({…})` belongs to the const, not to the file.
func TestMapToSymbols_TSConstInitializerBelongsToTheConst(t *testing.T) {
	t.Run("from the hunk", func(t *testing.T) {
		got := mapDoc(t, zodDoc(), fileDiff("src/lib.ts", "@@ -3,4 +3,5 @@\n export const profileSchema = z.object({\n   id: z.string(),\n   name: z.string(),\n+  custom: z.array(z.string()).default([]),\n });\n"))
		if len(got) != 1 || got[0].Name != "profileSchema" || got[0].Declaration != impact.DeclUnchanged {
			t.Fatalf("got %+v, want profileSchema with its declaration unchanged", got)
		}
	})
	t.Run("a changed key", func(t *testing.T) {
		got := mapDoc(t, zodDoc(), fileDiff("src/lib.ts", "@@ -3,4 +3,4 @@\n export const profileSchema = z.object({\n   id: z.string(),\n-  name: z.string(),\n+  name: z.string().min(1),\n });\n"))
		if len(got) != 1 || got[0].Name != "profileSchema" || got[0].DeletionUnresolved {
			t.Fatalf("got %+v, want profileSchema alone", got)
		}
	})
	t.Run("without the declaration line nothing says whose it is", func(t *testing.T) {
		got := mapDoc(t, zodDoc(), fileDiff("src/lib.ts", "@@ -5,2 +5,3 @@\n   name: z.string(),\n+  custom: z.array(z.string()).default([]),\n });\n"))
		if len(got) != 1 || got[0].SymbolID != "file:src/lib.ts" {
			t.Fatalf("got %+v, want the file-level entry", got)
		}
	})
}
