// Package pathclass says which repo paths hold generated files or data rather than
// code a person writes. History-based analyses (hotspots, co-change coupling, risk)
// leave those paths out: a benchmark directory committed in batches of fifty files
// is churn and coupling without meaning.
//
// The classes come from .ckb/config.json:
//
//	"paths": {"generated": ["gen/**"], "data": ["bench-data/**"]}
//
// review.generatedPatterns counts as generated too, and the built-in generated
// patterns of the review's generated check (DefaultGeneratedPatterns) always apply.
// A large data file (.json, .jsonl, .ndjson, .csv, .tsv over LargeDataBytes) is
// data without being declared.
package pathclass

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/config"
)

// Class names a kind of path left out of history-based analyses.
type Class string

const (
	// None is a path in no class: code, config, docs.
	None Class = ""
	// Generated is output of a tool: protobuf, bundles, lockfile-like output.
	Generated Class = "generated"
	// Data is checked-in data: measurements, fixtures, exports.
	Data Class = "data"
)

// LargeDataBytes is the size from which a data-format file counts as data without
// being declared. A config file is rarely a quarter megabyte; an export often is.
const LargeDataBytes = 256 * 1024

// dataExtensions are the formats the size rule applies to.
var dataExtensions = map[string]bool{
	".json": true, ".jsonl": true, ".ndjson": true, ".csv": true, ".tsv": true,
}

// DefaultGeneratedPatterns returns the built-in generated-file globs, the same the
// review's generated check starts from. A new slice each call: callers append.
func DefaultGeneratedPatterns() []string {
	return []string{
		// Generic generated markers
		"*.generated.*", "*_generated.go", "*_gen.go",
		// Protobuf/gRPC
		"*.pb.go", "*.pb.cc", "*.pb.h", "*.pb.ts", "*.pb.js",
		"*_grpc.pb.go", "*_pb2.py", "*_pb2_grpc.py",
		// Parser generators
		"parser.tab.c", "lex.yy.c",
		// API generators
		"*.swagger.json", "*.openapi.json",
		// Bundled/minified
		"*.min.js", "*.min.css", "*.bundle.js", "*.chunk.js",
		"**/dist/*.js", "**/dist/*.css",
		// Go generators
		"*_string.go", "*_enumer.go", "wire_gen.go", "*_mock.go",
		// Dart/Flutter
		"*.g.dart", "*.freezed.dart", "*.mocks.dart",
		// GraphQL
		"*__generated__*",
	}
}

// Classifier puts repo-relative paths into classes.
type Classifier struct {
	repoRoot  string // for the size rule; empty turns it off
	generated []string
	data      []string
}

// New returns a classifier for explicit pattern lists, on top of the defaults.
// repoRoot enables the large-data-file rule; pass "" to match by pattern only.
func New(repoRoot string, generated, data []string) *Classifier {
	return &Classifier{
		repoRoot:  repoRoot,
		generated: append(DefaultGeneratedPatterns(), clean(generated)...),
		data:      clean(data),
	}
}

// FromConfig builds the classifier a repo's config declares. A nil config gives
// the defaults.
func FromConfig(repoRoot string, cfg *config.Config) *Classifier {
	if cfg == nil {
		return New(repoRoot, nil, nil)
	}
	generated := append(append([]string{}, cfg.Paths.Generated...), cfg.Review.GeneratedPatterns...)
	return New(repoRoot, generated, cfg.Paths.Data)
}

// Load reads .ckb/config.json under repoRoot. A missing or unreadable config gives
// the defaults: leaving fewer files out is the safe failure.
func Load(repoRoot string) *Classifier {
	cfg, err := config.LoadConfig(repoRoot)
	if err != nil {
		cfg = nil
	}
	return FromConfig(repoRoot, cfg)
}

func clean(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(filepath.ToSlash(p))
		p = strings.TrimPrefix(p, "./")
		p = strings.TrimPrefix(p, "/")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Classify returns the class of a repo-relative path, and the pattern or rule that
// put it there. Declared data wins over generated, generated over the size rule.
func (c *Classifier) Classify(path string) (Class, string) {
	if c == nil || path == "" {
		return None, ""
	}
	path = strings.TrimPrefix(filepath.ToSlash(path), "./")
	for _, p := range c.data {
		if Match(p, path) {
			return Data, p
		}
	}
	for _, p := range c.generated {
		if Match(p, path) {
			return Generated, p
		}
	}
	if c.repoRoot != "" && dataExtensions[strings.ToLower(filepath.Ext(path))] {
		if info, err := os.Stat(filepath.Join(c.repoRoot, path)); err == nil && !info.IsDir() && info.Size() >= LargeDataBytes {
			return Data, "large data file"
		}
	}
	return None, ""
}

// Excluded says whether a path is in any class.
func (c *Classifier) Excluded(path string) bool {
	class, _ := c.Classify(path)
	return class != None
}

// Tally counts the paths an analysis left out, by class, for the response to say so.
type Tally struct {
	Count   int            `json:"count"`
	ByClass map[string]int `json:"byClass,omitempty"`
	// Examples are a few of the paths, sorted, so the reader can check the rule.
	Examples []string `json:"examples,omitempty"`
	Note     string   `json:"note"`
}

const maxTallyExamples = 3

// Counter collects left-out paths; each path counts once.
type Counter struct {
	seen    map[string]bool
	byClass map[Class]int
}

// Add records a path in a class. None is ignored.
func (n *Counter) Add(path string, class Class) {
	if class == None {
		return
	}
	if n.seen == nil {
		n.seen = make(map[string]bool)
		n.byClass = make(map[Class]int)
	}
	if n.seen[path] {
		return
	}
	n.seen[path] = true
	n.byClass[class]++
}

// Skip classifies a path with c, records it if it is in a class, and says whether
// to leave it out.
func (n *Counter) Skip(c *Classifier, path string) bool {
	class, _ := c.Classify(path)
	n.Add(path, class)
	return class != None
}

// Count is the number of distinct paths left out.
func (n *Counter) Count() int { return len(n.seen) }

// Tally returns the summary, or nil when nothing was left out.
func (n *Counter) Tally() *Tally {
	if n == nil || len(n.seen) == 0 {
		return nil
	}
	t := &Tally{Count: len(n.seen), ByClass: make(map[string]int, len(n.byClass))}
	for class, count := range n.byClass {
		t.ByClass[string(class)] = count
	}
	paths := make([]string, 0, len(n.seen))
	for p := range n.seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) > maxTallyExamples {
		paths = paths[:maxTallyExamples]
	}
	t.Examples = paths
	t.Note = n.Note()
	return t
}

// Note is the one-line statement of what was left out, e.g. "12 files in
// data/generated paths left out (data 10, generated 2)". Empty when nothing was.
func (n *Counter) Note() string {
	if n == nil || len(n.seen) == 0 {
		return ""
	}
	noun := "files"
	if len(n.seen) == 1 {
		noun = "file"
	}
	var parts []string
	for _, class := range []Class{Data, Generated} {
		if count := n.byClass[class]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", class, count))
		}
	}
	return fmt.Sprintf("%d %s in data/generated paths left out (%s; set in .ckb/config.json paths.data / paths.generated)",
		len(n.seen), noun, strings.Join(parts, ", "))
}

// Match reports whether a glob matches a repo-relative path. "*" matches across
// directories; "**" matches any number of directories; a pattern without a wildcard
// matches the path itself or anything under it as a directory ("bench-data",
// "bench-data/").
func Match(pattern, path string) bool {
	if !strings.Contains(pattern, "*") {
		dir := strings.TrimSuffix(pattern, "/")
		return path == pattern || path == dir || strings.HasPrefix(path, dir+"/")
	}
	if !strings.Contains(pattern, "**") {
		return matchSimple(pattern, path)
	}

	// Split on first ** occurrence only
	idx := strings.Index(pattern, "**")
	prefix := pattern[:idx]
	suffix := strings.TrimPrefix(pattern[idx+2:], "/")

	if prefix != "" && !strings.HasPrefix(path, prefix) {
		return false
	}
	if suffix == "" {
		return true
	}
	remaining := strings.TrimPrefix(path, prefix)

	// Try the suffix against every tail of the path: for suffix "dist/*.js" and
	// path "a/b/dist/index.js", try "a/b/dist/index.js", "b/dist/index.js", ...
	parts := strings.Split(remaining, "/")
	for i := range parts {
		candidate := strings.Join(parts[i:], "/")
		if strings.Contains(suffix, "**") {
			if Match(suffix, candidate) {
				return true
			}
		} else if matchSimple(suffix, candidate) {
			return true
		}
	}
	return false
}

// matchSimple matches a pattern with * wildcards against a string.
func matchSimple(pattern, str string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == str
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 2 {
		return len(str) >= len(parts[0])+len(parts[1]) && strings.HasPrefix(str, parts[0]) && strings.HasSuffix(str, parts[1])
	}
	// The first part anchors the start, the last the end, the rest appear in order.
	if !strings.HasPrefix(str, parts[0]) {
		return false
	}
	remaining := str[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		if part == "" {
			continue
		}
		idx := strings.Index(remaining, part)
		if idx < 0 {
			return false
		}
		remaining = remaining[idx+len(part):]
	}
	return strings.HasSuffix(remaining, last)
}
