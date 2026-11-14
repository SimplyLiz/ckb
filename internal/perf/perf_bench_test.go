package perf

import (
	"fmt"
	"testing"
)

// =============================================================================
// perf package benchmarks
// =============================================================================
// These cover the hot paths of the two scan modes:
//
//   recordCommit         — O(files²) pair-building called once per commit
//   buildCoChangePairs   — full git-log parse loop (simulated, no git I/O)
//   shouldIgnore         — per-file path filter on every commit
//   CorrelationLevel     — per-pair level classification
//   ScanPipeline         — composite: pair-building + correlation + ignore filter
//
// Baselines (Apple M4 Pro, arm64, -count=1 -benchmem):
//   recordCommit/2files:            ~79 ns/op,      0 B/op,   0 allocs/op
//   recordCommit/5files:           ~497 ns/op,    744 B/op,   3 allocs/op
//   recordCommit/10files:         ~2.7 µs/op,   5816 B/op,  13 allocs/op
//   recordCommit/20files:          ~11 µs/op,  23992 B/op,  21 allocs/op
//   recordCommit/50files:          ~74 µs/op, 197000 B/op,  34 allocs/op
//   recordCommit_Reuse/10files:   ~1.0 µs/op,      0 B/op,   0 allocs/op  ← reused maps
//   recordCommit_WithIgnored:      ~522 ns/op,    744 B/op,   3 allocs/op  ← 5 real + 5 ignored
//   shouldIgnore/ignored:         ~1.8 ns/op,      0 B/op,   0 allocs/op
//   shouldIgnore/not_ignored:     ~7.1 ns/op,      0 B/op,   0 allocs/op
//   CorrelationLevel:            ~0.26 ns/op,      0 B/op,   0 allocs/op
//   CoChangePipeline/100c_5f:     ~38 µs/op,   55888 B/op,  12 allocs/op
//   CoChangePipeline/500c_10f:   ~630 µs/op,  406802 B/op,  29 allocs/op  ← seen-map lifted (was 1526)
//   CoChangePipeline/1kc_20f:    ~4.8 ms/op, 1586994 B/op,  75 allocs/op  ← seen-map lifted (was 3072)
//   correlationFilter/~20kpairs: ~372 µs/op,      0 B/op,   0 allocs/op
//
// Notable: recordCommit is O(files²) per commit — the dominant cost on repos
// with large commits (fmt sweeps, mass renames). The seen map is allocated once
// in buildCoChangePairs and reused across commits (range-delete to clear),
// reducing CoChangePipeline allocs by ~97% at 1k commits. git output is parsed
// via bufio.Scanner to avoid loading the full log into memory before processing.
// The ignore filter cuts pairing work by dropping testdata/vendor before O(n²).
//
// Use benchstat for before/after comparison:
//   go test -bench=. -benchmem -count=6 -run=^$ ./internal/perf > before.txt
//   # make changes
//   go test -bench=. -benchmem -count=6 -run=^$ ./internal/perf > after.txt
//   benchstat before.txt after.txt
// =============================================================================

// BenchmarkRecordCommit measures the O(files²) pair-building cost at increasing
// file counts. Large commits (e.g. formatting sweeps, mass renames) are the
// dominant cost in buildCoChangePairs.
func BenchmarkRecordCommit(b *testing.B) {
	a := &Analyzer{}
	sizes := []int{2, 5, 10, 20, 50}

	for _, n := range sizes {
		files := make([]string, n)
		for i := range files {
			files[i] = fmt.Sprintf("internal/pkg%d/file%d.go", i%10, i)
		}

		b.Run(fmt.Sprintf("%dfiles", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pairs := make(map[filePair]int)
				totals := make(map[string]int)
				seen := make(map[string]bool)
				a.recordCommit(files, pairs, totals, seen)
			}
		})
	}
}

// BenchmarkRecordCommit_Reuse measures recordCommit when the maps are reused
// across calls (as in buildCoChangePairs). Avoids measuring map allocation.
// BenchmarkRecordCommit_Reuse measures the steady-state cost with all maps
// pre-allocated and reused across calls — what buildCoChangePairs actually does.
func BenchmarkRecordCommit_Reuse(b *testing.B) {
	a := &Analyzer{}
	files := make([]string, 10)
	for i := range files {
		files[i] = fmt.Sprintf("internal/pkg%d/file.go", i)
	}
	pairs := make(map[filePair]int, 64)
	totals := make(map[string]int, 64)
	seen := make(map[string]bool, 16)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.recordCommit(files, pairs, totals, seen)
	}
}

// BenchmarkRecordCommit_WithIgnored measures the cost when a fraction of files
// are in testdata/ or vendor/ — the ignore filter should cut pairing work.
func BenchmarkRecordCommit_WithIgnored(b *testing.B) {
	a := &Analyzer{}
	// 5 real files + 5 ignored files per commit.
	files := []string{
		"internal/api/handler.go",
		"internal/query/engine.go",
		"internal/mcp/tools.go",
		"internal/storage/db.go",
		"internal/audit/analyzer.go",
		"testdata/fixtures/go/expected/symbol.json",
		"testdata/fixtures/go/expected/refs.json",
		"vendor/github.com/spf13/cobra/command.go",
		"vendor/github.com/spf13/cobra/args.go",
		"node_modules/lodash/lodash.js",
	}
	seen := make(map[string]bool, 8)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pairs := make(map[filePair]int)
		totals := make(map[string]int)
		a.recordCommit(files, pairs, totals, seen)
	}
}

// BenchmarkCoChangePipelineSimulated benchmarks the full pair-building inner
// loop without git I/O by calling recordCommit in a loop over synthetic commit
// batches. This is the dominant CPU cost during a Scan call.
//
// Sizes represent realistic repo histories:
//   - 100 commits × 5 files  ≈ focused feature branch
//   - 500 commits × 10 files ≈ mid-size service, 6 months history
//   - 1k  commits × 20 files ≈ busy monorepo module, 1 year history
func BenchmarkCoChangePipelineSimulated(b *testing.B) {
	a := &Analyzer{}

	scenarios := []struct {
		name    string
		commits int
		files   int
	}{
		{"100commits_5files", 100, 5},
		{"500commits_10files", 500, 10},
		{"1kcommits_20files", 1_000, 20},
	}

	for _, sc := range scenarios {
		// Pre-build commit batches so the benchmark doesn't measure string alloc.
		batches := make([][]string, sc.commits)
		for c := range batches {
			batch := make([]string, sc.files)
			for f := range batch {
				batch[f] = fmt.Sprintf("internal/pkg%d/file%d.go", f%8, (c+f)%15)
			}
			batches[c] = batch
		}

		b.Run(sc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pairs := make(map[filePair]int, sc.files*(sc.files-1)/2)
				totals := make(map[string]int, sc.files*2)
				seen := make(map[string]bool, sc.files)
				for _, batch := range batches {
					a.recordCommit(batch, pairs, totals, seen)
				}
			}
		})
	}
}

// BenchmarkShouldIgnore measures the path-prefix filter called on every file
// in every commit. Should be branch-predictor-friendly (most files don't match).
func BenchmarkShouldIgnore(b *testing.B) {
	cases := []struct {
		name string
		path string
	}{
		{"ignored_testdata", "testdata/fixtures/go/expected/symbol.json"},
		{"ignored_vendor", "vendor/github.com/spf13/cobra/command.go"},
		{"not_ignored_internal", "internal/query/engine.go"},
		{"not_ignored_cmd", "cmd/ckb/main.go"},
		{"not_ignored_testfile", "internal/perf/analyzer_test.go"},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				shouldIgnore(c.path)
			}
		})
	}
}

// BenchmarkCorrelationLevel measures the hot classification call made once per
// surviving candidate pair.
func BenchmarkCorrelationLevel(b *testing.B) {
	values := []float64{1.0, 0.9, 0.8, 0.7, 0.5, 0.4, 0.3, 0.1}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CorrelationLevel(values[i%len(values)])
	}
}

// BenchmarkCorrelationFilter measures the candidate filtering pass: iterate pairs,
// apply minCorrelation + minCoChanges, compute correlation. This is the in-memory
// work after buildCoChangePairs.
func BenchmarkCorrelationFilter(b *testing.B) {
	// Build a realistic pair map: 200 files × 200 files / 2 = ~20k pairs.
	const nFiles = 200
	pairCounts := make(map[filePair]int, nFiles*nFiles/2)
	fileTotals := make(map[string]int, nFiles)

	for i := 0; i < nFiles; i++ {
		f := fmt.Sprintf("internal/pkg%d/file%d.go", i%10, i)
		fileTotals[f] = 5 + (i % 20)
		for j := i + 1; j < nFiles; j++ {
			g := fmt.Sprintf("internal/pkg%d/file%d.go", j%10, j)
			if i < j {
				pairCounts[filePair{f, g}] = 1 + (i+j)%8
			}
		}
	}

	const minCorrelation = 0.3
	const minCoChanges = 3

	b.ReportAllocs()
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		var kept int
		for pair, count := range pairCounts {
			if count < minCoChanges {
				continue
			}
			totalA := fileTotals[pair.a]
			totalB := fileTotals[pair.b]
			minTotal := totalA
			if totalB < minTotal {
				minTotal = totalB
			}
			if minTotal == 0 {
				continue
			}
			corr := float64(count) / float64(minTotal)
			if corr >= minCorrelation {
				kept++
			}
		}
		_ = kept
	}
}
