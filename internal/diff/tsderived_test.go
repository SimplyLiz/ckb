package diff

import (
	"strconv"
	"strings"
	"testing"
)

func TestTSTypeofAlias(t *testing.T) {
	src := strings.Split(`export type Profile = z.infer<typeof profileSchema>;
export type Kind = (typeof KINDS)[number];
export type Row = typeof users.$inferSelect;
export type Out = ReturnType<typeof build>;
export type Wide = {
  profile: z.infer<typeof profileSchema>;
};
export interface Held {
  kind: (typeof KINDS)[number];
}
export const sample: z.infer<typeof profileSchema> = {};
export const again = profileSchema.parse(raw);
function f(p: z.infer<typeof profileSchema>) {}`, "\n")
	cases := []struct {
		line int
		name string
		want string
	}{
		{1, "profileSchema", "Profile"},
		{2, "KINDS", "Kind"},
		{3, "users", "Row"},
		{4, "build", "Out"},
		{6, "profileSchema", "Wide"},
		{9, "KINDS", "Held"},
		{11, "profileSchema", ""}, // A variable annotated with it, no alias
		{12, "profileSchema", ""}, // No typeof
		{13, "profileSchema", ""}, // A parameter
	}
	for _, tc := range cases {
		got, ok := TSTypeofAlias(src, tc.line, 0, tc.name)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("line %d: alias = %q %v, want %q", tc.line, got, ok, tc.want)
		}
	}
}

func TestTSEnclosingValue(t *testing.T) {
	src := strings.Split(`export const projectSchema = z
  .object({
    profile: profileSchema,
    resumes: z.array(resumeSchema).min(1),
  })
  .refine((p) => check(p, profileSchema));

export const patch = profileSchema.partial();
export const parse = (raw: unknown) => profileSchema.parse(raw);
export const parseLong = (
  raw: unknown,
) => profileSchema.parse(raw);
export function load() {
  return profileSchema.parse(read());
}
const after = 1;`, "\n")
	cases := []struct {
		line          int
		name, key     string
		wantEnclosing bool
	}{
		{3, "projectSchema", "profile", true},
		{4, "projectSchema", "resumes", true},
		{6, "projectSchema", "", true},
		{8, "patch", "", true},
		{9, "", "", false},  // An arrow function uses the schema, its type doesn't follow it
		{12, "", "", false}, // The same over several lines
		{14, "", "", false}, // A function declaration
	}
	for _, tc := range cases {
		name, key, ok := TSEnclosingValue(src, tc.line)
		if name != tc.name || key != tc.key || ok != tc.wantEnclosing {
			t.Errorf("line %d: got %q %q %v, want %q %q %v", tc.line, name, key, ok, tc.name, tc.key, tc.wantEnclosing)
		}
	}
}

func TestTSRecordLiteral(t *testing.T) {
	src := strings.Split(`const LABELS: Record<Kind, string> = {
  a: "A",
};
const ICONS: Partial<Record<Kind, Icon>> = { a: X };
const count: Record<string, Kind> = { a: "a" };
function labels(): Record<Kind, string> {
  return { a: "A" };
}
const typed = (k: Kind): Record<Kind, number> => ({ a: 1 });
let later: Record<Kind, string>;
type Labels = Record<Kind, string>;`, "\n")
	for line, want := range map[int]int{1: 3, 4: 4, 5: 0, 6: 8, 9: 9, 10: 0, 11: 0} {
		column := 0
		if line == 9 {
			column = strings.Index(src[8], "Record<Kind")
		}
		end, ok := TSRecordLiteral(src, line, column, "Kind")
		if end != want || ok != (want != 0) {
			t.Errorf("line %d: end = %d %v, want %d", line, end, ok, want)
		}
	}
}

func TestTSLiteralKeyLine(t *testing.T) {
	src := strings.Split(`export const project: Project = {
  id: "1",
  resumes: [{ profile: "nested deeper" }],
  profile: {
    id: "2",
  },
};
export function empty(): Project {
  const profile = load();
  return {
    id: "3",
    profile,
  };
}
const spread: Project = { ...base, id: profile ? a : b };`, "\n")
	cases := []struct {
		first, last int
		key         string
		want        int
	}{
		{1, 7, "profile", 4},
		{1, 7, "id", 2},
		{1, 7, "custom", 0},
		{8, 14, "profile", 12}, // Shorthand, in the returned literal
		{15, 15, "profile", 0}, // A value, not a key
	}
	for _, tc := range cases {
		if got := TSLiteralKeyLine(src, tc.first, tc.last, tc.key); got != tc.want {
			t.Errorf("%d..%d %s: line = %d, want %d", tc.first, tc.last, tc.key, got, tc.want)
		}
	}
}

func TestTSLiteralsMissing(t *testing.T) {
	src := strings.Split(`export const a: Profile = {
  id: "1",
};
export const b: Profile = { ...a, id: "2" };
export const c: Profile = { id: "3", custom: [] };
export function make(): Profile {
  const base = load();
  return {
    id: "4",
  };
}
export function pass(): Profile {
  return load();
}
const list: Profile[] = [{ id: "5" }, { id: "6", custom: [] }];
const cast = { id: "7" } as Profile;
export const project: Project = {
  name: "x",
  profile: {
    id: "8",
  },
};
export const other: Project = { name: "y", profile: a };
const arrow = (): Profile => ({ id: "9" });
const rows: Project[] = [
  { name: "z", profile: { id: "10", custom: [] } },
  { name: "w", profile: { id: "11" } },
];
let later: Profile;`, "\n")
	cases := []struct {
		line int
		name string
		path []string
		want string
		read bool
	}{
		{1, "Profile", nil, "[1]", true},
		{4, "Profile", nil, "[]", true},  // The spread carries it
		{5, "Profile", nil, "[]", true},  // Set
		{6, "Profile", nil, "[8]", true}, // The returned literal
		{12, "Profile", nil, "[]", true}, // Built elsewhere
		{15, "Profile", nil, "[15]", true},
		{16, "Profile", nil, "[]", true}, // A cast compiles without the key
		{17, "Project", []string{"profile"}, "[19]", true},
		{23, "Project", []string{"profile"}, "[]", true}, // A value from elsewhere
		{24, "Profile", nil, "[24]", true},
		{25, "Project", []string{"profile"}, "[27]", true},
		{29, "Profile", nil, "[]", false}, // Nothing is built here
	}
	for _, tc := range cases {
		at, read := TSLiteralsMissing(src, tc.line, 0, tc.name, tc.path, []string{"custom"})
		if got := fmtInts(at); got != tc.want || read != tc.read {
			t.Errorf("line %d: missing at %s read %v, want %s read %v", tc.line, got, read, tc.want, tc.read)
		}
	}
}

func fmtInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return "[" + strings.Join(parts, " ") + "]"
}
