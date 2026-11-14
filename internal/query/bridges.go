package query

import (
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/coupling"
	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
)

// A bridge is a source file whose removal cuts the code graph apart so that at
// least two pieces keep bridgeMinCut files each: a change there reaches two
// parts of the code that meet nowhere else. Betweenness does not find these —
// it flagged 96 of 618 files of a Next.js app, nearly all merely central.
//
// The graph is undirected over the index's source files, without tests,
// generated files and build output (Next.js's .next/types/validator.ts
// references every page and would make the whole app one biconnected block). Two files
// are joined when one references a symbol the other defines (any language the
// index covers) or imports it (TypeScript/Python import sites, side-effect
// imports included). Ported from Cartographer's bridge_cuts (aebfbd4), which
// worked on its own import resolution.

// bridgeMinCut is the smallest cut that makes a bridge. Measured on a 618-file
// TS app (2026-11-14): 39 articulation points without test edges, nearly all
// the only importer of one or two leaf files; from 3 on one is left.
const bridgeMinCut = 3

var bridgeCache = struct {
	sync.Mutex
	byIndex map[*scip.SCIPIndex]map[string]int
}{byIndex: map[*scip.SCIPIndex]map[string]int{}}

// codeBridges returns the bridges of idx, file → files it cuts off (>=
// bridgeMinCut), computed once per loaded index.
func codeBridges(idx *scip.SCIPIndex, repoRoot string, classes *pathclass.Classifier) map[string]int {
	if idx == nil {
		return nil
	}
	bridgeCache.Lock()
	if b, ok := bridgeCache.byIndex[idx]; ok {
		bridgeCache.Unlock()
		return b
	}
	bridgeCache.Unlock()

	ids, adj := codeFileGraph(idx, fileImportGraph(idx, repoRoot), func(p string) bool {
		return isTestFilePath(p) || coupling.IsNoisePath(p) || classes.Excluded(p)
	})
	bridges := map[string]int{}
	for id, cut := range bridgeCuts(ids, adj) {
		if cut >= bridgeMinCut {
			bridges[id] = cut
		}
	}

	bridgeCache.Lock()
	defer bridgeCache.Unlock()
	// An index is replaced, not mutated, on reindex: drop results of old ones.
	for k := range bridgeCache.byIndex {
		delete(bridgeCache.byIndex, k)
	}
	bridgeCache.byIndex[idx] = bridges
	return bridges
}

// codeFileGraph is the undirected file graph of idx: sorted file ids and their
// neighbour lists (deduplicated, no self loops). Files skip reports stay out.
func codeFileGraph(idx *scip.SCIPIndex, imports *importGraph, skip func(string) bool) ([]string, [][]int) {
	var ids []string
	for _, doc := range idx.Documents {
		if doc != nil && !skip(doc.RelativePath) {
			ids = append(ids, doc.RelativePath)
		}
	}
	sort.Strings(ids)
	ids = dedupSorted(ids)
	index := make(map[string]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	sets := make([]map[int]struct{}, len(ids))
	link := func(a, b string) {
		i, ok1 := index[a]
		j, ok2 := index[b]
		if !ok1 || !ok2 || i == j {
			return
		}
		if sets[i] == nil {
			sets[i] = map[int]struct{}{}
		}
		if sets[j] == nil {
			sets[j] = map[int]struct{}{}
		}
		sets[i][j] = struct{}{}
		sets[j][i] = struct{}{}
	}

	for _, doc := range idx.Documents {
		if doc == nil {
			continue
		}
		if _, ok := index[doc.RelativePath]; !ok {
			continue
		}
		for _, occ := range doc.Occurrences {
			if occ == nil || occ.SymbolRoles&scip.SymbolRoleDefinition != 0 || strings.HasPrefix(occ.Symbol, "local ") {
				continue
			}
			def := idx.DefinitionIndex[occ.Symbol]
			if def == nil || def.Doc == nil {
				continue
			}
			link(doc.RelativePath, def.Doc.RelativePath)
		}
	}
	if imports != nil {
		for from, tos := range imports.edges {
			for to := range tos {
				link(from, to)
			}
		}
	}

	adj := make([][]int, len(ids))
	for i, s := range sets {
		for j := range s {
			adj[i] = append(adj[i], j)
		}
		sort.Ints(adj[i])
	}
	return ids, adj
}

func dedupSorted(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// bridgeCuts returns the articulation points of the undirected graph (ids,
// adj) with how many files each one cuts off: the size of the second-largest
// piece left after removing it (the largest piece is what stays; the
// second-largest is the floor for what is cut off). Nodes that are no
// articulation point are absent.
//
// The files of a Go package are one compilation unit; each package with two or
// more files gets a weightless hub node joining them, so cmd/x/main.go does not
// hang off the rest of package main through whatever one file both reference.
// The hub counts in no piece size and is never reported.
//
// Tarjan's lowpoint DFS with subtree sizes, iterative so a deep import chain
// cannot overflow the stack. A DFS child w of u with low[w] >= disc[u] is a
// piece of its own once u is gone; what is left of the component besides
// those pieces and u hangs on through u's DFS parent and is one more piece.
func bridgeCuts(ids []string, adjIn [][]int) map[string]int {
	n := len(ids)
	adj := make([][]int, n, n+n/4)
	copy(adj, adjIn)
	weight := make([]int, n, n+n/4)
	for i := range weight {
		weight[i] = 1
	}
	goPkgs := map[string][]int{}
	var pkgOrder []string
	for i, id := range ids {
		if strings.HasSuffix(id, ".go") {
			dir := path.Dir(id)
			if _, ok := goPkgs[dir]; !ok {
				pkgOrder = append(pkgOrder, dir)
			}
			goPkgs[dir] = append(goPkgs[dir], i)
		}
	}
	for _, dir := range pkgOrder {
		members := goPkgs[dir]
		if len(members) < 2 {
			continue
		}
		hub := len(adj)
		adj = append(adj, append([]int(nil), members...))
		weight = append(weight, 0)
		for _, m := range members {
			// Copy before appending: adjIn's slices belong to the caller.
			adj[m] = append(append([]int(nil), adj[m]...), hub)
		}
	}
	n = len(adj)

	const none = -1
	disc := make([]int, n)
	low := make([]int, n)
	parent := make([]int, n)
	next := make([]int, n)
	size := append([]int(nil), weight...)
	pieces := make([]int, n)
	pieceSum := make([]int, n)
	top2 := make([][2]int, n)
	for i := range disc {
		disc[i] = none
		parent[i] = none
	}
	order := make([]int, 0, n)
	var stack []int
	cuts := map[string]int{}

	for root := 0; root < n; root++ {
		if disc[root] != none {
			continue
		}
		start := len(order)
		disc[root] = len(order)
		low[root] = disc[root]
		order = append(order, root)
		stack = append(stack, root)
		for len(stack) > 0 {
			u := stack[len(stack)-1]
			if next[u] < len(adj[u]) {
				w := adj[u][next[u]]
				next[u]++
				if disc[w] == none {
					parent[w] = u
					disc[w] = len(order)
					low[w] = disc[w]
					order = append(order, w)
					stack = append(stack, w)
				} else if w != parent[u] {
					low[u] = min(low[u], disc[w])
				}
				continue
			}
			stack = stack[:len(stack)-1]
			p := parent[u]
			if p == none {
				continue
			}
			size[p] += size[u]
			low[p] = min(low[p], low[u])
			if low[u] >= disc[p] && size[u] > 0 { // a piece of only package hubs holds no file
				pieces[p]++
				pieceSum[p] += size[u]
				t := &top2[p]
				if size[u] > t[0] {
					t[1], t[0] = t[0], size[u]
				} else if size[u] > t[1] {
					t[1] = size[u]
				}
			}
		}
		component := size[root]
		for _, u := range order[start:] {
			if pieces[u] == 0 || weight[u] == 0 {
				continue
			}
			// The root has no rest: all its DFS children are pieces.
			rest := component - 1 - pieceSum[u]
			count := pieces[u]
			if rest > 0 {
				count++
			}
			if count < 2 {
				continue
			}
			s := []int{top2[u][0], top2[u][1], rest}
			sort.Sort(sort.Reverse(sort.IntSlice(s)))
			cuts[ids[u]] = s[1]
		}
	}
	return cuts
}
