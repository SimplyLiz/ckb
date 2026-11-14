package query

import (
	"context"
	"math"
	"sort"
)

// ShotgunEntry is one file whose changes historically spread across many
// others: the shotgun-surgery smell.
type ShotgunEntry struct {
	File           string `json:"file"`
	Partners       int    `json:"partners"`       // files it changed together with, in >= minHiddenCoChanges commits each
	TotalCoChanges int    `json:"totalCoChanges"` // shared commits summed over those partners
	// Entropy (bits) of the co-changes over the partners: high when they are
	// spread evenly, low when one partner dominates.
	Entropy float64 `json:"entropy"`
	// Score is Partners relative to the most dispersed file, 0-100.
	Score float64 `json:"score"`
}

// ShotgunSurgeryResponse ranks files by co-change dispersion.
type ShotgunSurgeryResponse struct {
	Files          []ShotgunEntry `json:"files"`
	CommitsRead    int            `json:"commitsRead"`
	MinPartners    int            `json:"minPartners"`
	MinCoChanges   int            `json:"minCoChanges"`
	NotAssessed    string         `json:"notAssessed,omitempty"`
	TotalQualified int            `json:"totalQualified"`
}

// ShotgunSurgery ranks source files by how many others they change together
// with, from the co-change history review uses (bots, formatting-only commits,
// generated files and vendored code left out). Cartographer's dispersion
// (git_analysis.rs dispersion_from_pairs) on coupling.History; unlike it, a
// partner needs minHiddenCoChanges shared commits, not one, and tests take no
// part — a test changing with its subject is no smell.
func (e *Engine) ShotgunSurgery(ctx context.Context, minPartners, limit int) *ShotgunSurgeryResponse {
	if minPartners <= 0 {
		minPartners = 3
	}
	if limit <= 0 {
		limit = 20
	}
	resp := &ShotgunSurgeryResponse{MinPartners: minPartners, MinCoChanges: minHiddenCoChanges, Files: []ShotgunEntry{}}
	h := e.cochangeHistory(ctx)
	if h == nil {
		resp.NotAssessed = "no git history"
		return resp
	}
	resp.CommitsRead = h.Commits()
	code := func(f string) bool { return isConceptSourceFile(f) && !isTestFilePath(f) }
	partners := map[string]map[string]int{}
	add := func(a, b string, n int) {
		if partners[a] == nil {
			partners[a] = map[string]int{}
		}
		partners[a][b] += n
	}
	for _, p := range h.AllPairs(minHiddenCoChanges) {
		if code(p.FileA) && code(p.FileB) {
			add(p.FileA, p.FileB, p.Count)
			add(p.FileB, p.FileA, p.Count)
		}
	}
	resp.Files = shotgunRank(partners, minPartners)
	resp.TotalQualified = len(resp.Files)
	if len(resp.Files) > limit {
		resp.Files = resp.Files[:limit]
	}
	return resp
}

// shotgunRank turns file → partner → shared commits into entries with at
// least minPartners partners, most dispersed first.
func shotgunRank(partners map[string]map[string]int, minPartners int) []ShotgunEntry {
	maxPartners := 1
	for _, ps := range partners {
		maxPartners = max(maxPartners, len(ps))
	}
	out := []ShotgunEntry{}
	for file, ps := range partners {
		if len(ps) < minPartners {
			continue
		}
		total := 0
		for _, n := range ps {
			total += n
		}
		entropy := 0.0
		for _, n := range ps {
			p := float64(n) / float64(total)
			entropy -= p * math.Log2(p)
		}
		out = append(out, ShotgunEntry{
			File:           file,
			Partners:       len(ps),
			TotalCoChanges: total,
			Entropy:        math.Round(entropy*100) / 100,
			Score:          math.Round(float64(len(ps)) / float64(maxPartners) * 100),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Partners != out[j].Partners {
			return out[i].Partners > out[j].Partners
		}
		if out[i].Entropy != out[j].Entropy {
			return out[i].Entropy > out[j].Entropy
		}
		return out[i].File < out[j].File
	})
	return out
}
