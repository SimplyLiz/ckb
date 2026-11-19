package scip

import "testing"

// TestParseSCIPIdentifier_DescriptorAware exercises ParseSCIPIdentifier end
// to end (not by hand-building a *SCIPIdentifier struct literal, which
// bypasses the real bindings-backed parser and only exercises the legacy
// string-heuristic fallback) against realistic, spec-conformant SCIP
// identifiers across languages, verifying GetSimpleName, ExtractSymbolKind,
// and GetContainerName together.
//
// This is a regression suite for a bug where ckb impact prepare reported
// unexported Go methods (e.g. Engine#buildProvenance) as "public": the old
// hand-rolled descriptor parser split a method descriptor on the last '/'
// only, so `pkg`/Engine#buildProvenance(). returned the simple name
// "Engine#buildProvenance" — the capitalized receiver type leaked in, and
// inferVisibility's Go export-by-case test saw the uppercase 'E' from
// "Engine" and called every method on an exported type "public" regardless
// of the method's own case. A follow-on fix (unconditionally stripping
// "Type#" from the simple name) then broke every other language: for
// TypeScript it made "Handler#handle" collapse to "handle" for indexing
// purposes too, TS parameter descriptors like "Handler#handle().(input)"
// got misclassified as "method" (both '#' and '(' present), and
// backtick-escaped names like "`<constructor>`" or a receiver-and-name
// with a dotted module path in between produced garbage or empty names.
//
// The fix replaces the hand-rolled parser with the official
// github.com/sourcegraph/scip/bindings/go/scip parser, which splits a
// symbol into scheme / package (manager, name, version) / a chain of
// descriptors each carrying its own suffix kind (namespace, type, term,
// method, type-parameter, parameter, meta, macro), correctly unescaping
// backtick-quoted names. GetSimpleName is then simply "the last
// descriptor's name" (no receiver leakage, no cross-language ambiguity);
// ExtractSymbolKind looks at the last descriptor's suffix (checking
// whether its *parent* is a type to distinguish method vs. function);
// GetContainerName walks back for the nearest enclosing type descriptor.
func TestParseSCIPIdentifier_DescriptorAware(t *testing.T) {
	tests := []struct {
		name          string
		symbol        string
		wantName      string
		wantKind      SymbolKind
		wantContainer string
	}{
		{
			name:          "go: unexported method on exported receiver type",
			symbol:        "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/query`/Engine#buildProvenance().",
			wantName:      "buildProvenance",
			wantKind:      KindMethod,
			wantContainer: "Engine",
		},
		{
			name:          "go: exported method on exported receiver type",
			symbol:        "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/query`/Engine#BuildProvenance().",
			wantName:      "BuildProvenance",
			wantKind:      KindMethod,
			wantContainer: "Engine",
		},
		{
			name:          "go: package-level function (no receiver)",
			symbol:        "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/api`/NewServer().",
			wantName:      "NewServer",
			wantKind:      KindFunction,
			wantContainer: "",
		},
		{
			name:          "go: bare exported type (no parens)",
			symbol:        "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/api`/Server#",
			wantName:      "Server",
			wantKind:      KindClass,
			wantContainer: "",
		},
		{
			name:          "typescript: method on a type — must not collapse to the receiver",
			symbol:        "scip-typescript npm fixture 1.0.0 src/pkg/`handler.ts`/Handler#handle().",
			wantName:      "handle",
			wantKind:      KindMethod,
			wantContainer: "Handler",
		},
		{
			name:          "typescript: constructor with escaped name",
			symbol:        "scip-typescript npm fixture 1.0.0 src/pkg/`handler.ts`/Handler#`<constructor>`().",
			wantName:      "<constructor>",
			wantKind:      KindMethod,
			wantContainer: "Handler",
		},
		{
			name:          "typescript: parameter descriptor — must not be classified as method",
			symbol:        "scip-typescript npm fixture 1.0.0 src/pkg/`handler.ts`/Handler#handle().(input)",
			wantName:      "input",
			wantKind:      KindParameter,
			wantContainer: "Handler",
		},
		{
			name:          "typescript: free function (no receiver)",
			symbol:        "scip-typescript npm fixture 1.0.0 src/pkg/`handler.ts`/newHandler().",
			wantName:      "newHandler",
			wantKind:      KindFunction,
			wantContainer: "",
		},
		{
			name:          "java: maven package coordinate (name field has an escaped internal space)",
			symbol:        "scip-java maven com.google.guava  guava 31.0 ImmutableList#builder().",
			wantName:      "builder",
			wantKind:      KindMethod,
			wantContainer: "ImmutableList",
		},
		{
			name:          "python: nested module attribute",
			symbol:        "scip-python pypi requests 2.28.0 requests/models.py/Response#json().",
			wantName:      "json",
			wantKind:      KindMethod,
			wantContainer: "Response",
		},
		// The rust cases below are verbatim symbols from a real
		// `rust-analyzer scip` run over a crate with `struct Foo`,
		// `impl Foo { fn bar, fn new }`, `trait Greet` and
		// `impl Greet for Foo { fn hi }`. rust-analyzer does not emit
		// "Foo#bar()." for impl members; it emits "impl#[Foo]bar().".
		{
			name:          "rust-analyzer: inherent impl method",
			symbol:        "rust-analyzer cargo demo 0.1.0 impl#[Foo]bar().",
			wantName:      "bar",
			wantKind:      KindMethod,
			wantContainer: "Foo",
		},
		{
			name:          "rust-analyzer: inherent impl associated fn",
			symbol:        "rust-analyzer cargo demo 0.1.0 impl#[Foo]new().",
			wantName:      "new",
			wantKind:      KindMethod,
			wantContainer: "Foo",
		},
		{
			name:          "rust-analyzer: trait impl method (self type, then trait)",
			symbol:        "rust-analyzer cargo demo 0.1.0 impl#[Foo][Greet]hi().",
			wantName:      "hi",
			wantKind:      KindMethod,
			wantContainer: "Foo",
		},
		{
			name:          "rust-analyzer: trait declaration method",
			symbol:        "rust-analyzer cargo demo 0.1.0 Greet#hi().",
			wantName:      "hi",
			wantKind:      KindMethod,
			wantContainer: "Greet",
		},
		{
			name:          "rust-analyzer: free function",
			symbol:        "rust-analyzer cargo demo 0.1.0 free().",
			wantName:      "free",
			wantKind:      KindFunction,
			wantContainer: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := ParseSCIPIdentifier(tt.symbol)
			if err != nil {
				t.Fatalf("ParseSCIPIdentifier(%q) failed: %v", tt.symbol, err)
			}
			if got := id.GetSimpleName(); got != tt.wantName {
				t.Errorf("GetSimpleName() = %q, want %q", got, tt.wantName)
			}
			if got := id.ExtractSymbolKind(); got != tt.wantKind {
				t.Errorf("ExtractSymbolKind() = %q, want %q", got, tt.wantKind)
			}
			if got := id.GetContainerName(); got != tt.wantContainer {
				t.Errorf("GetContainerName() = %q, want %q", got, tt.wantContainer)
			}
		})
	}
}

// TestParseSCIPIdentifier_JavaPackageCoordinate is a regression test for a
// bug where ParseSCIPIdentifier's SplitN(id, " ", 5) treated Java's
// two-token "groupId artifactId" package coordinate as name+version,
// shifting the real version into the descriptor (producing names like
// "0 ImmutableList#method" instead of "method" for direct-dependent
// resolution). The SCIP spec escapes an internal space in a package field
// by doubling it; the official bindings parser (which ParseSCIPIdentifier
// now delegates to) handles that per spec.
func TestParseSCIPIdentifier_JavaPackageCoordinate(t *testing.T) {
	id, err := ParseSCIPIdentifier("scip-java maven com.google.guava  guava 31.0 ImmutableList#builder().")
	if err != nil {
		t.Fatalf("ParseSCIPIdentifier failed: %v", err)
	}
	if id.Manager != "maven" {
		t.Errorf("Manager = %q, want %q", id.Manager, "maven")
	}
	if id.Package != "com.google.guava guava" {
		t.Errorf("Package = %q, want %q", id.Package, "com.google.guava guava")
	}
	if id.Version != "31.0" {
		t.Errorf("Version = %q, want %q", id.Version, "31.0")
	}
	if got := id.GetSimpleName(); got != "builder" {
		t.Errorf("GetSimpleName() = %q, want %q", got, "builder")
	}
}

// TestInferVisibility_GoUnexportedMethod is a regression test at the point
// the parser bug actually surfaced: inferVisibility used to see the
// mangled "Engine#buildProvenance" simple name, read the uppercase 'E' from
// the receiver type, and reported the unexported method as "public".
func TestInferVisibility_GoUnexportedMethod(t *testing.T) {
	id, err := ParseSCIPIdentifier("scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/query`/Engine#buildProvenance().")
	if err != nil {
		t.Fatalf("ParseSCIPIdentifier failed: %v", err)
	}

	name := id.GetSimpleName()
	got := inferVisibility(&SymbolInformation{}, id, name)
	if got != "private" {
		t.Errorf("inferVisibility(%q) = %q, want %q", name, got, "private")
	}
}

// TestGetSimpleName_LegacyFallback verifies that malformed/legacy
// identifiers which don't conform to the strict SCIP grammar (e.g. missing
// the package-version field) still parse via the lenient fallback instead
// of erroring out, preserving tolerance for ad-hoc test fixtures and any
// non-conformant real-world index data.
func TestGetSimpleName_LegacyFallback(t *testing.T) {
	id, err := ParseSCIPIdentifier("scip-go go ckb/internal/query Engine#Close().")
	if err != nil {
		t.Fatalf("ParseSCIPIdentifier failed: %v", err)
	}
	// The legacy fallback (no package-version field, so the official
	// parser rejects it) never had the receiver-stripping fix — it
	// keeps the pre-fix behavior of the hand-rolled splitter, which is
	// fine: this path only exists for non-conformant identifiers, and
	// real index data always carries a package version.
	if got := id.GetSimpleName(); got != "Engine#Close" {
		t.Errorf("GetSimpleName() = %q, want %q", got, "Engine#Close")
	}
}

// An exported method name on an unexported Go type (engine#Build) is not
// public API: it must not trigger the "Public API change" risk factor.
func TestInferVisibility_GoExportedMethodOnUnexportedType(t *testing.T) {
	cases := map[string]string{
		"scip-go gomod demo v0 `demo/pkg`/engine#Build().": "private",
		"scip-go gomod demo v0 `demo/pkg`/Engine#Build().": "public",
		"scip-go gomod demo v0 `demo/pkg`/Build().":        "public",
	}
	for sym, want := range cases {
		id, err := ParseSCIPIdentifier(sym)
		if err != nil {
			t.Fatal(err)
		}
		if got := inferVisibility(&SymbolInformation{}, id, id.GetSimpleName()); got != want {
			t.Errorf("%s: visibility = %q, want %q", sym, got, want)
		}
	}
}

// scip-python emits one "<module>/__init__:" symbol per module for top-level
// statements. It is a Meta descriptor, which used to read as a type and so
// passed the namespace guard that keeps file-scope pseudo-symbols out of
// caller lists. (Symbol shapes follow scip-python's output format.)
func TestModuleScopeSymbols(t *testing.T) {
	scope := []string{
		"scip-python python myapp 0.1.0 `app.main`/__init__:",
		"scip-python python myapp 0.1.0 `app`/__init__:",
		"scip-typescript npm fixture 1.0.0 src/`handler.ts`/",
	}
	for _, s := range scope {
		id, err := ParseSCIPIdentifier(s)
		if err != nil {
			t.Fatal(err)
		}
		if k := id.ExtractSymbolKind(); k != KindNamespace {
			t.Errorf("%s: kind = %q, want namespace", s, k)
		}
		if !IsModuleScopeSymbol(s) {
			t.Errorf("%s should be module scope", s)
		}
	}
	for _, s := range []string{
		"scip-python python myapp 0.1.0 `app.main`/handler().",
		"scip-python python myapp 0.1.0 `app.main`/Server#serve().",
		"scip-python python myapp 0.1.0 `app.main`/Server#",
		"local 3",
	} {
		if IsModuleScopeSymbol(s) {
			t.Errorf("%s must not be module scope", s)
		}
	}
}

func TestFindCallers_SkipsModuleScope(t *testing.T) {
	const callee = "scip-python python myapp 0.1.0 `app.util`/helper()."
	const fn = "scip-python python myapp 0.1.0 `app.main`/run()."
	const mod = "scip-python python myapp 0.1.0 `app.main`/__init__:"
	idx := &SCIPIndex{CallerIndex: map[string][]string{callee: {mod, fn}}}
	idx.callerIndexOnce.Do(func() {})
	callers, err := idx.FindCallers(callee)
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 1 || callers[0].SymbolID != fn {
		t.Errorf("callers = %+v, want only run()", callers)
	}
}
