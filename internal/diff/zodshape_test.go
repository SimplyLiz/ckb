package diff

import (
	"strings"
	"testing"
)

const zodOld = `import { z } from "zod";

const id = z.string().min(1);
const text = (max = 500) => z.string().max(max).default("");

export const itemSchema = z.object({
  id,
  label: z.string(),
});

export const profileSchema = z.object({
  id,
  /** A note. */
  name: z.string().min(1),
  title: text(100),
  kind: z.enum(KINDS),
  nick: z.string().optional(),
  items: z.array(itemSchema).max(10),
});

export const patchSchema = profileSchema.partial().extend({ rev: z.number() });
export const headSchema = profileSchema.pick({ id: true, name: true });
export const bodySchema = profileSchema.omit({ id: true }).merge(itemSchema);
export const kindSchema = z.enum(["a", "b"]);
export const eventSchema = z.discriminatedUnion("type", [openSchema, closeSchema]);
export const eitherSchema = z.union([z.string(), z.number()]);
export const wrapped = z
  .object({
    profile: profileSchema,
  })
  .refine((p) => p.profile.id !== "");
export const moved = z.object({ a: z.string() }).transform((v) => v.a);
export const foreign = otherSchema.extend({ a: z.string() });
export const plain = z.string();
export const KINDS = ["a", "b"] as const;
`

func zodChange(t *testing.T, name, from, to string) ZodChange {
	t.Helper()
	if !strings.Contains(zodOld, from) {
		t.Fatalf("fixture lacks %q", from)
	}
	change, ok := ZodSchemaChange(strings.Split(zodOld, "\n"), strings.Split(strings.Replace(zodOld, from, to, 1), "\n"), name)
	if !ok {
		t.Fatalf("%s: not read as a zod schema", name)
	}
	return change
}

func TestZodSchemaChange_Keys(t *testing.T) {
	cases := []struct {
		name, schema, from, to string
		want                   string // The change, one line per key
		literals               string // Keys a literal of the output type now has to set
	}{
		{"default is required in the output", "profileSchema", "  nick: z.string().optional(),\n",
			"  nick: z.string().optional(),\n  custom: z.array(itemSchema).max(10).default([]),\n",
			"+ custom (required in output, default [])", "custom"},
		{"optional key", "profileSchema", "  nick: z.string().optional(),\n",
			"  nick: z.string().optional(),\n  note: z.string().optional(),\n", "+ note (optional)", ""},
		{"required key", "profileSchema", "  kind: z.enum(KINDS),\n", "  kind: z.enum(KINDS),\n  age: z.number(),\n", "+ age (required)", "age"},
		{"helper with a default", "profileSchema", "  title: text(100),\n", "  title: text(100),\n  motto: text(),\n",
			"+ motto (required in output, default \"\")", "motto"},
		{"removed", "profileSchema", "  nick: z.string().optional(),\n", "", "- nick", ""},
		{"retyped", "profileSchema", "  kind: z.enum(KINDS),\n", "  kind: z.union([z.enum(KINDS), custom]),\n",
			"~ kind: z.enum(KINDS) -> z.union([z.enum(KINDS), custom]) (wider)", ""},
		{"a constraint is no type", "profileSchema", "z.array(itemSchema).max(10)", "z.array(itemSchema).max(20)", "", ""},
		{"now optional", "profileSchema", "  name: z.string().min(1),\n", "  name: z.string().min(1).optional(),\n", "~ name: now optional", ""},
		{"now required", "profileSchema", "  nick: z.string().optional(),\n", "  nick: z.string(),\n", "~ nick: now required", "nick"},
		{"shorthand added", "itemSchema", "  label: z.string(),\n", "  label: z.string(),\n  rank,\n", "+ rank (required)", "rank"},
		{"through partial and extend", "patchSchema", "{ rev: z.number() }", "{ rev: z.number(), by: z.string() }", "+ by (required)", "by"},
		{"through the base of partial", "patchSchema", "  kind: z.enum(KINDS),\n", "  kind: z.enum(KINDS),\n  age: z.number(),\n", "+ age (optional)", ""},
		{"pick leaves the key out", "headSchema", "  kind: z.enum(KINDS),\n", "  kind: z.enum(KINDS),\n  age: z.number(),\n", "", ""},
		{"pick takes one more", "headSchema", "{ id: true, name: true }", "{ id: true, name: true, nick: true }", "+ nick (optional)", ""},
		{"omit and merge", "bodySchema", "  label: z.string(),\n", "  label: z.string(),\n  rank: z.number(),\n", "+ rank (required)", "rank"},
		{"refine doesn't hide the keys", "wrapped", "    profile: profileSchema,\n", "    profile: profileSchema,\n    rev: z.number(),\n", "+ rev (required)", "rev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			change := zodChange(t, tc.schema, tc.from, tc.to)
			if !change.Exact {
				t.Fatalf("not exact: %s", change.Unread)
			}
			if got := strings.Join(change.Lines(), "\n"); got != tc.want {
				t.Errorf("change = %q, want %q", got, tc.want)
			}
			if got := strings.Join(change.LiteralKeys(), " "); got != tc.literals {
				t.Errorf("literal keys = %q, want %q", got, tc.literals)
			}
		})
	}
}

func TestZodSchemaChange_Members(t *testing.T) {
	change := zodChange(t, "kindSchema", `["a", "b"]`, `["a", "c", "d"]`)
	if got := strings.Join(change.Lines(), "\n"); !change.Exact || got != "+ \"c\"\n+ \"d\"\n- \"b\"" {
		t.Errorf("enum change = %q exact %v", got, change.Exact)
	}
	if !change.MembersAdded() || !change.MembersRemoved() {
		t.Errorf("enum: added %v removed %v, want both", change.MembersAdded(), change.MembersRemoved())
	}
	change = zodChange(t, "eventSchema", "[openSchema, closeSchema]", "[openSchema, closeSchema, moveSchema]")
	if got := strings.Join(change.Lines(), "\n"); !change.Exact || got != "+ moveSchema" || change.MembersRemoved() {
		t.Errorf("discriminated union change = %q", got)
	}
	change = zodChange(t, "eitherSchema", "[z.string(), z.number()]", "[z.string(), z.boolean()]")
	if got := strings.Join(change.Lines(), "\n"); got != "+ z.boolean()\n- z.number()" {
		t.Errorf("union change = %q", got)
	}
}

// What the reader can't follow it says, and the caller falls back to listing every literal.
func TestZodSchemaChange_Unread(t *testing.T) {
	for schema, edit := range map[string][2]string{
		"moved":   {"{ a: z.string() }", "{ a: z.string(), b: z.string() }"},
		"foreign": {"{ a: z.string() }", "{ a: z.string(), b: z.string() }"},
		"plain":   {"z.string();\nexport const KINDS", "z.number();\nexport const KINDS"},
	} {
		change := zodChange(t, schema, edit[0], edit[1])
		if change.Exact || change.Unread == "" {
			t.Errorf("%s: exact %v unread %q, want a reason it wasn't read", schema, change.Exact, change.Unread)
		}
	}
	lines := strings.Split(zodOld, "\n")
	if _, ok := ZodSchemaChange(lines, lines, "KINDS"); ok {
		t.Error("an as-const array is no zod schema")
	}
	if _, ok := ZodSchemaChange(lines, lines, "missing"); ok {
		t.Error("a name the file doesn't declare is no zod schema")
	}
}

func TestZodKeyLine(t *testing.T) {
	lines := strings.Split(zodOld, "\n")
	for key, want := range map[string]int{"name": 14, "kind": 16, "id": 12, "missing": 0} {
		if got := ZodKeyLine(lines, "profileSchema", key); got != want {
			t.Errorf("line of %s = %d, want %d", key, got, want)
		}
	}
	if got := ZodKeyLine(lines, "wrapped", "profile"); got != 29 {
		t.Errorf("line of wrapped.profile = %d, want 29", got)
	}
}
