package query

import (
	"math"
	"testing"
)

func TestShotgunRank(t *testing.T) {
	partners := map[string]map[string]int{
		"hub.go": {"a.go": 4, "b.go": 4, "c.go": 4, "d.go": 4},
		"one.go": {"a.go": 9, "b.go": 1, "c.go": 1},
		"two.go": {"a.go": 3, "b.go": 3},
	}
	got := shotgunRank(partners, 3)
	if len(got) != 2 || got[0].File != "hub.go" || got[1].File != "one.go" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Score != 100 || got[1].Score != 75 || got[0].TotalCoChanges != 16 {
		t.Errorf("scores/totals: %+v", got)
	}
	// Four even partners: 2 bits. One dominant partner: well below log2(3).
	if math.Abs(got[0].Entropy-2) > 1e-9 || got[1].Entropy >= 1.0 {
		t.Errorf("entropy: %+v", got)
	}
}
