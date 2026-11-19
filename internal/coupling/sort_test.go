package coupling

import (
	"fmt"
	"testing"
)

func TestSortCorrelations_TiesHaveAFixedOrder(t *testing.T) {
	want := "[a.ts b.ts c.ts d.ts e.ts]"
	for run := 0; run < 20; run++ {
		cs := []Correlation{
			{FilePath: "e.ts", Correlation: 0.5, CoChangeCount: 2},
			{FilePath: "c.ts", Correlation: 0.75, CoChangeCount: 3},
			{FilePath: "d.ts", Correlation: 0.75, CoChangeCount: 2},
			{FilePath: "b.ts", Correlation: 0.75, CoChangeCount: 3},
			{FilePath: "a.ts", Correlation: 1, CoChangeCount: 1},
		}
		// Start from a different permutation each run, as a map would.
		for i := 0; i < run%len(cs); i++ {
			cs = append(cs[1:], cs[0])
		}
		sortCorrelations(cs)
		var got []string
		for _, c := range cs {
			got = append(got, c.FilePath)
		}
		if fmt.Sprint(got) != want {
			t.Fatalf("run %d: order = %v, want %s", run, got, want)
		}
	}
}
