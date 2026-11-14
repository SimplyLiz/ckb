package mcp

import "testing"

// as asserts v's dynamic type and fails the test instead of panicking when it
// is something else; the MCP results these tests unpack are untyped maps.
func as[T any](t *testing.T, v any) T {
	t.Helper()
	got, ok := v.(T)
	if !ok {
		var want T
		t.Fatalf("value is %T, want %T: %v", v, want, v)
	}
	return got
}
