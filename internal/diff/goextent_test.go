package diff

import (
	"strings"
	"testing"
)

func linesOf(src string) map[int]string {
	lines := make(map[int]string)
	for i, line := range strings.Split(src, "\n") {
		lines[i+1] = line
	}
	return lines
}

func TestGoValueEnd(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int // Last line of the value on line 1; 0 for none
	}{
		{"composite literal", "var Table = map[string]int{\n\t\"a\": 1,\n}\n\nfunc f() {}", 3},
		{"one line", "var x = 1\nvar y = 2", 1},
		{"trailing comment", "var x = 1 // a note\nvar y = 2", 1},
		{"raw string over lines", "var Long = `a\nb\nc`\nvar y = 2", 3},
		{"raw string in a literal", "var cmd = &Command{\n\tLong: `x\n  y`,\n\tRun: run,\n}\nvar z = 1", 5},
		{"concatenation", "var s = \"a\" +\n\t\"b\" +\n\t\"c\"\nvar y = 2", 3},
		{"call with a comment inside", "var x = f(\n\ta, // why\n\tb)\nvar y = 2", 3},
		{"brace in a string", "var x = \"{\"\nvar y = 2", 1},
		{"rune", "var x = '('\nvar y = 2", 1},
		{"comparison", "var ok = a == b\nvar y = 2", 1},
		{"typed", "var p T = T{\n\tA: 1,\n}", 3},
		{"in a group", "\tA = T{\n\t\tB: 1,\n\t}\n\tC = 2\n)", 3},
		{"group closed on its line", "\tA = 1)", 1},
		{"no initializer", "var x int\nvar y = 2", 0},
		{"anonymous struct type", "var x struct {\n\tA int\n}", 0},
		{"unclosed", "var x = []int{\n\t1,", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := newGoSource(linesOf(tc.src)).valueEnd(1); got != tc.want {
				t.Errorf("valueEnd = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("a line the side doesn't show", func(t *testing.T) {
		lines := linesOf("var Table = map[string]int{\n\t\"a\": 1,\n}")
		delete(lines, 2)
		if got := newGoSource(lines).valueEnd(1); got != 0 {
			t.Errorf("valueEnd = %d, want 0: the gap could hold anything", got)
		}
	})
}

func TestIsGoPackageValue(t *testing.T) {
	pkg := "scip-go gomod example.com/demo abc `example.com/demo/lib`/"
	for symbol, want := range map[string]bool{
		pkg + "Table.":     true,
		pkg + "Point#X.":   false,
		pkg + "Point#":     false,
		pkg + "Target().":  false,
		pkg + "Point#M().": false,
		pkg:                false,
	} {
		if got := isGoPackageValue(symbol); got != want {
			t.Errorf("isGoPackageValue(%q) = %v, want %v", symbol, got, want)
		}
	}
}
