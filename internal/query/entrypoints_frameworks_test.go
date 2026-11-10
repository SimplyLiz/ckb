package query

import (
	"fmt"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

func TestNextEntrypointType(t *testing.T) {
	cases := map[string]string{
		"src/app/(app)/editor/page.tsx":      "page",
		"app/layout.tsx":                     "page",
		"src/app/api/projects/[id]/route.ts": "api",
		"src/app/global-error.tsx":           "page",
		"src/proxy.ts":                       "api", // Next 16's middleware
		"middleware.ts":                      "api",
		"pages/api/hello.ts":                 "api",
		"src/pages/index.tsx":                "page",
		"src/app/editor/EditorShell.tsx":     "", // A component, called by a page
		"src/lib/proxy.ts":                   "", // Only at the root
		"src/app/api/route.test.ts":          "",
		"src/app/styles.css":                 "",
	}
	for path, want := range cases {
		if got := nextEntrypointType(path); got != want {
			t.Errorf("nextEntrypointType(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestNextCalledExports(t *testing.T) {
	src := `import { x } from "y";
export default async function Page() {}
export async function GET(req: Request) {}
export const POST = handler;
export function helper() {}
export const generateMetadata = async () => ({});
const handler = make();
export { handler as PUT, handler as PATCH, other };
`
	names, anon := nextCalledExports(src)
	if fmt.Sprint(names) != "[handler Page GET POST generateMetadata]" || anon {
		t.Errorf("got %v anonymous=%v", names, anon)
	}
	if _, anon := nextCalledExports("export default function () {}\n"); !anon {
		t.Error("anonymous default export not seen")
	}
}

func TestProjectCallees_SkipsExternalDeclarations(t *testing.T) {
	idx := &scip.SCIPIndex{DefinitionIndex: map[string]*scip.OccurrenceRef{
		"own/a().": {}, "own/b().": {}, "own/c().": {},
	}}
	nodes := []*scip.CallGraphNode{
		{SymbolID: "lib/`lib.es5.d.ts`/String#trim()."},
		{SymbolID: "own/a()."},
		{SymbolID: "lib/`lib.es5.d.ts`/Array#map()."},
		{SymbolID: "own/b()."},
		{SymbolID: "own/c()."},
	}
	if got := projectCallees(nodes, idx, 2); fmt.Sprint(got) != "[own/a(). own/b().]" {
		t.Errorf("got %v", got)
	}
}
