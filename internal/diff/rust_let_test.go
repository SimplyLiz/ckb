package diff

import "testing"

func TestRustLetAt(t *testing.T) {
	src := lines(`fn f(resolved: Resolved) -> Result<(), E> {
    let Proposal::Admit { handle_id } = resolved.action();
    let Wrapper(Proposal::Admit { handle_id }, _) = w;
    let p: Proposal = Proposal::Admit { handle_id: 1 };
    if let Proposal::Admit { handle_id } = p {}
    while let Some(Proposal::Admit { .. }) = it.next() {}
    let Proposal::Admit { handle_id } = p else { return Err(E) };
    let Proposal::Admit { handle_id } = if c { a } else { b };
    let x = Proposal::Admit { handle_id: 2 };
    let Proposal::Admit {
        handle_id,
    } = p;
    if ok && let Proposal::Admit { .. } = p {}
}`)
	tests := []struct {
		line int
		want bool
	}{
		{2, true},   // the plain irrefutable let
		{3, true},   // nested in a tuple struct
		{4, false},  // type and initializer, not the pattern
		{5, false},  // if let
		{6, false},  // while let
		{7, false},  // let … else
		{8, true},   // an if/else initializer is no let-else
		{9, false},  // initializer
		{10, true},  // pattern over several lines
		{13, false}, // let chain
	}
	for _, tt := range tests {
		got, ok := RustLetAt(src, tt.line, 0, "Admit")
		if ok != tt.want || ok && got != tt.line {
			t.Errorf("line %d: got %d %v, want %v", tt.line, got, ok, tt.want)
		}
	}
}
