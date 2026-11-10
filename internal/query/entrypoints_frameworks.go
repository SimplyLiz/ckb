package query

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// nextRouteFiles are the files Next.js itself calls, by base name without extension.
var nextRouteFiles = map[string]string{
	"page": "page", "layout": "page", "template": "page", "loading": "page", "error": "page",
	"not-found": "page", "global-error": "page", "default": "page", "route": "api",
}

var nextExtensions = map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true}

// isNextApp reports whether the repo is a Next.js app: a next config, or next in
// package.json's dependencies.
func isNextApp(repoRoot string) bool {
	for _, name := range []string{"next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs"} {
		if _, err := os.Stat(filepath.Join(repoRoot, name)); err == nil {
			return true
		}
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	_, dep := pkg.Dependencies["next"]
	_, dev := pkg.DevDependencies["next"]
	return dep || dev
}

// nextEntrypointType returns what Next.js calls a file for ("page", "api"), or ""
// if it calls nothing in it: app router files (page, layout, route, ...), pages
// router files, and middleware or proxy (Next 16's name for it) at the root.
func nextEntrypointType(path string) string {
	ext := filepath.Ext(path)
	if !nextExtensions[ext] || isTestFilePath(path) {
		return ""
	}
	rel := strings.TrimPrefix(filepath.ToSlash(path), "src/")
	base := strings.TrimSuffix(filepath.Base(rel), ext)
	switch {
	case rel == "middleware"+ext || rel == "proxy"+ext || rel == "instrumentation"+ext:
		return "api"
	case strings.HasPrefix(rel, "app/"):
		return nextRouteFiles[base]
	case strings.HasPrefix(rel, "pages/api/"):
		return "api"
	case strings.HasPrefix(rel, "pages/"):
		return "page"
	}
	return ""
}

// nextExport matches the exports Next.js calls: the default export, HTTP method
// handlers, middleware/proxy and the metadata/params hooks.
var nextExport = regexp.MustCompile(`(?m)^export\s+(?:(default)\s+)?(?:async\s+)?(?:function\s*\*?\s*|const\s+|let\s+)([A-Za-z_$][\w$]*)?`)

var nextCalledNames = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true,
	"middleware": true, "proxy": true, "register": true, "generateMetadata": true, "generateStaticParams": true,
	"getServerSideProps": true, "getStaticProps": true, "getStaticPaths": true,
}

// nextExportList matches an export list, export { handler as GET, POST }.
var nextExportList = regexp.MustCompile(`(?m)^export\s*\{([^}]*)\}`)

// nextCalledExports returns the local names of what Next.js calls in source (for
// export { handler as GET } that is handler), and whether the default export is
// anonymous.
func nextCalledExports(source string) (names []string, anonymousDefault bool) {
	for _, m := range nextExportList.FindAllStringSubmatch(source, -1) {
		for _, item := range strings.Split(m[1], ",") {
			fields := strings.Fields(item)
			local, exported := "", ""
			switch {
			case len(fields) == 1:
				local, exported = fields[0], fields[0]
			case len(fields) == 3 && fields[1] == "as":
				local, exported = fields[0], fields[2]
			}
			if local != "" && (nextCalledNames[exported] || exported == "default") && !containsString(names, local) {
				names = append(names, local)
			}
		}
	}
	for _, m := range nextExport.FindAllStringSubmatch(source, -1) {
		isDefault, name := m[1] != "", m[2]
		switch {
		case isDefault && name == "":
			anonymousDefault = true
		case (isDefault || nextCalledNames[name]) && !containsString(names, name):
			names = append(names, name)
		}
	}
	return names, anonymousDefault
}

// frameworkEntrypoints finds the files a framework calls by convention, which no
// naming rule sees: a Next.js page is a default export called Page, a route
// handler is called GET. Each called export is an entrypoint; a file whose called
// export can't be resolved to a symbol is listed once, under its module symbol.
func (e *Engine) frameworkEntrypoints(idx *scip.SCIPIndex) []EntrypointV52 {
	if idx == nil || !isNextApp(e.repoRoot) {
		return nil
	}
	var out []EntrypointV52
	for _, doc := range idx.Documents {
		kind := nextEntrypointType(doc.RelativePath)
		if kind == "" {
			continue
		}
		source, err := os.ReadFile(filepath.Join(e.repoRoot, doc.RelativePath))
		if err != nil {
			continue
		}
		names, anonymous := nextCalledExports(string(source))
		byName := make(map[string]*scip.SymbolInformation)
		module := ""
		for _, sym := range doc.Symbols {
			if sym == nil || strings.HasPrefix(sym.Symbol, "local ") {
				continue
			}
			if strings.HasSuffix(sym.Symbol, "/") {
				module = sym.Symbol
				continue
			}
			if name := scip.SymbolSimpleName(sym.Symbol); name != "" && byName[name] == nil {
				byName[name] = sym
			}
		}
		found := 0
		for _, name := range names {
			sym := byName[name]
			if sym == nil {
				continue
			}
			found++
			out = append(out, EntrypointV52{
				SymbolId:       sym.Symbol,
				Name:           name,
				Type:           kind,
				Location:       &LocationInfo{FileId: doc.RelativePath, StartLine: definitionLine(idx, sym.Symbol)},
				DetectionBasis: "framework-convention",
				FanOut:         idx.GetCalleeCount(sym.Symbol),
			})
		}
		if found == 0 && (anonymous || len(names) > 0) && module != "" {
			out = append(out, EntrypointV52{
				SymbolId:       module,
				Name:           filepath.Base(doc.RelativePath),
				Type:           kind,
				Location:       &LocationInfo{FileId: doc.RelativePath},
				DetectionBasis: "framework-convention",
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Location.FileId < out[j].Location.FileId })
	return out
}

// definitionLine is the 1-based line a symbol is defined on, or 0.
func definitionLine(idx *scip.SCIPIndex, symbol string) int {
	if ref := idx.DefinitionIndex[symbol]; ref != nil && ref.Occ != nil && len(ref.Occ.Range) > 0 {
		return int(ref.Occ.Range[0]) + 1
	}
	return 0
}

func containsString(list []string, s string) bool {
	for _, have := range list {
		if have == s {
			return true
		}
	}
	return false
}
