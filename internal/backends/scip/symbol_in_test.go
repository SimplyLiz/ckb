package scip

import "testing"

// A name at a crate's root has no module before it: rust-analyzer writes
// `… app 1.5.0 Commands#`, not `…/Commands#`.
func TestSymbolInFindsCrateRootNames(t *testing.T) {
	def := func(sym string) *Occurrence {
		return &Occurrence{Range: []int32{0, 0, 1}, Symbol: sym, SymbolRoles: SymbolRoleDefinition}
	}
	main := &Document{RelativePath: "src/main.rs", Occurrences: []*Occurrence{
		def("rust-analyzer cargo app 1.5.0 Commands#"),
		def("rust-analyzer cargo app 1.5.0 Commands#Map#"),
		def("rust-analyzer cargo app 1.5.0 MAX."),
		def("rust-analyzer cargo app 1.5.0 SubCommands#"),
	}}
	cli := &Document{RelativePath: "src/cli.rs", Occurrences: []*Occurrence{def("rust-analyzer cargo app 1.5.0 cli/Args#")}}
	adapter := &SCIPAdapter{index: &SCIPIndex{DocumentsByPath: map[string]*Document{main.RelativePath: main, cli.RelativePath: cli}}}

	if got := adapter.TypeSymbolIn("src/main.rs", "Commands"); got != "rust-analyzer cargo app 1.5.0 Commands#" {
		t.Errorf("crate root type = %q", got)
	}
	if got := adapter.TypeSymbolIn("src/cli.rs", "Args"); got != "rust-analyzer cargo app 1.5.0 cli/Args#" {
		t.Errorf("module type = %q", got)
	}
	if got := adapter.ValueSymbolIn("src/main.rs", "MAX"); got != "rust-analyzer cargo app 1.5.0 MAX." {
		t.Errorf("crate root value = %q", got)
	}
	if v := adapter.VariantSymbolsIn("src/main.rs", adapter.TypeSymbolIn("src/main.rs", "Commands")); v["Map"] == "" {
		t.Errorf("variants = %v", v)
	}
}
