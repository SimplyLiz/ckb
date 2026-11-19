package query

import (
	"fmt"
	"sort"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// cutsOf runs bridgeCuts on an edge list; nodes lists files without edges too.
func cutsOf(nodes []string, edges [][2]string) map[string]int {
	set := map[string]bool{}
	for _, n := range nodes {
		set[n] = true
	}
	for _, e := range edges {
		set[e[0]], set[e[1]] = true, true
	}
	var ids []string
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	index := map[string]int{}
	for i, id := range ids {
		index[id] = i
	}
	adj := make([][]int, len(ids))
	for _, e := range edges {
		a, b := index[e[0]], index[e[1]]
		adj[a] = append(adj[a], b)
		adj[b] = append(adj[b], a)
	}
	return bridgeCuts(ids, adj)
}

func chain(ids ...string) [][2]string {
	var out [][2]string
	for i := 1; i < len(ids); i++ {
		out = append(out, [2]string{ids[i-1], ids[i]})
	}
	return out
}

func TestBridgeCuts(t *testing.T) {
	t.Run("file joining two clusters", func(t *testing.T) {
		edges := [][2]string{
			{"a1.ts", "a2.ts"}, {"a1.ts", "a3.ts"}, {"a2.ts", "a3.ts"},
			{"b1.ts", "b2.ts"}, {"b1.ts", "b3.ts"}, {"b2.ts", "b3.ts"},
			{"x.ts", "a1.ts"}, {"x.ts", "b1.ts"},
		}
		c := cutsOf(nil, edges)
		if c["x.ts"] != 3 || c["a1.ts"] != 2 {
			t.Fatalf("x=3 a1=2 expected, got %v", c)
		}
	})

	t.Run("star centre cuts off one file", func(t *testing.T) {
		var edges [][2]string
		for i := 1; i <= 5; i++ {
			edges = append(edges, [2]string{"hub.ts", fmt.Sprintf("l%d.ts", i)})
		}
		if c := cutsOf(nil, edges); c["hub.ts"] != 1 || len(c) != 1 {
			t.Fatalf("hub=1 only, got %v", c)
		}
	})

	t.Run("ring has none", func(t *testing.T) {
		edges := chain("a.ts", "b.ts", "c.ts", "d.ts", "e.ts", "f.ts", "a.ts")
		if c := cutsOf(nil, edges); len(c) != 0 {
			t.Fatalf("no articulation points expected, got %v", c)
		}
	})

	t.Run("path, piece above the DFS parent", func(t *testing.T) {
		c := cutsOf(nil, chain("a.ts", "b.ts", "c.ts", "d.ts", "e.ts", "f.ts", "g.ts"))
		want := map[string]int{"b.ts": 1, "c.ts": 2, "d.ts": 3, "e.ts": 2, "f.ts": 1}
		if fmt.Sprint(c) != fmt.Sprint(want) {
			t.Fatalf("got %v, want %v", c, want)
		}
	})

	t.Run("DFS root joining three arms", func(t *testing.T) {
		// "0.ts" sorts first, so it is the DFS root; arms of 3, 4 and 5.
		var edges [][2]string
		for _, arm := range []struct {
			name string
			n    int
		}{{"p", 3}, {"q", 4}, {"r", 5}} {
			prev := "0.ts"
			for k := 0; k < arm.n; k++ {
				id := fmt.Sprintf("%s%d.ts", arm.name, k)
				edges = append(edges, [2]string{prev, id})
				prev = id
			}
		}
		if c := cutsOf(nil, edges); c["0.ts"] != 4 {
			t.Fatalf("0.ts cuts off 4, got %v", c)
		}
	})

	t.Run("go package files hold together", func(t *testing.T) {
		// main.go and root.go are one package and never reference each
		// other here; both reference v/v.go. Without the package hub v.go
		// would split {main, u/*} from {root, q/*}.
		edges := [][2]string{{"cmd/m/main.go", "v/v.go"}, {"cmd/m/root.go", "v/v.go"}}
		for _, pkg := range []string{"u", "q"} {
			owner := "cmd/m/main.go"
			if pkg == "q" {
				owner = "cmd/m/root.go"
			}
			for _, f := range []string{"a", "b", "c"} {
				id := pkg + "/" + f + ".go"
				edges = append(edges, [2]string{owner, id}, [2]string{id, "v/v.go"})
			}
		}
		if c := cutsOf(nil, edges); c["v/v.go"] != 0 {
			t.Fatalf("v/v.go is no articulation point, got %v", c)
		}
	})

	t.Run("package hub weighs nothing", func(t *testing.T) {
		// x/d.go joins two 3-file packages whose files reach it only
		// through one of them.
		edges := [][2]string{{"p/a.go", "x/d.go"}, {"r/a.go", "x/d.go"}}
		nodes := []string{"p/b.go", "p/c.go", "r/b.go", "r/c.go"}
		c := cutsOf(nodes, edges)
		if c["x/d.go"] != 3 || c["p/a.go"] != 2 {
			t.Fatalf("x/d.go=3 p/a.go=2 expected, got %v", c)
		}
		for id := range c {
			if id == "" {
				t.Fatalf("hub reported: %v", c)
			}
		}
	})
}

// codeFileGraph joins files by cross-file references and imports, skips
// definitions, locals and the files skip names.
func TestCodeFileGraph(t *testing.T) {
	def := func(sym string) *scip.Occurrence {
		return &scip.Occurrence{Symbol: sym, SymbolRoles: scip.SymbolRoleDefinition}
	}
	ref := func(sym string) *scip.Occurrence { return &scip.Occurrence{Symbol: sym} }
	a := &scip.Document{RelativePath: "a.go", Occurrences: []*scip.Occurrence{def("A"), ref("B"), ref("local 1")}}
	b := &scip.Document{RelativePath: "b.go", Occurrences: []*scip.Occurrence{def("B"), def("local 1"), ref("B")}}
	c := &scip.Document{RelativePath: "c.go", Occurrences: []*scip.Occurrence{def("C")}}
	tst := &scip.Document{RelativePath: "c_test.go", Occurrences: []*scip.Occurrence{ref("A"), ref("C")}}
	idx := &scip.SCIPIndex{
		Documents: []*scip.Document{a, b, c, tst},
		DefinitionIndex: map[string]*scip.OccurrenceRef{
			"A":       {Doc: a, Occ: a.Occurrences[0]},
			"B":       {Doc: b, Occ: b.Occurrences[0]},
			"C":       {Doc: c, Occ: c.Occurrences[0]},
			"local 1": {Doc: b, Occ: b.Occurrences[1]},
		},
	}
	ids, adj := codeFileGraph(idx, &importGraph{edges: map[string]map[string]importKind{"c.go": {"a.go": importStatic}}}, isTestFilePath)
	got := map[string][]string{}
	for i, nb := range adj {
		for _, j := range nb {
			got[ids[i]] = append(got[ids[i]], ids[j])
		}
	}
	want := "map[a.go:[b.go c.go] b.go:[a.go] c.go:[a.go]]"
	if fmt.Sprint(got) != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}
