//go:build cartographer

package cartographer

// Integration test for the FFI contract between CKB and the linked Cartographer
// library: every bridge function CKB calls runs against a small git repository
// and the decoded structs must carry the fields CKB reads.
//
// Passes against the pinned lib v4.1.0. v4.0.3 and older serialized several
// Rust structs without `#[serde(rename_all = "camelCase")]` (ProjectGraph nodes
// and metadata, SimulatedChange, LayerViolation, shotgun and evolution entries),
// so `module_id`, `is_bridge`, `health_score`, `god_modules`, `predicted_impact`,
// `source_path`, `partner_count` and friends decoded as zero values into the
// camelCase tags in types.go. Nothing noticed: CKB just shipped zeros (measured
// 2026-11-14). If this fails again, fix Cartographer, not types.go: do not
// switch the tags to snake_case to make it pass.
//
// Run: go test -tags cartographer ./internal/cartographer/ -run TestFFIContract -v

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureRepo builds a small Go + TypeScript repository with dated commits:
//
//	ui/handler.go  -> db/store.go     (Go import that skips the svc layer)
//	svc/service.go -> db/store.go
//	web/ui/view.ts -> web/db/model.ts (TS import that skips web/svc)
//	web/svc/api.ts -> web/db/model.ts
//
// svc/service.go and web/svc/api.ts change together in five commits without any
// import between them (hidden coupling); web/svc/api.ts also changes with many
// other files (shotgun surgery). layers.toml sits at the root where Cartographer's
// own rebuild looks for it.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}

	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-40 * 24 * time.Hour)
	commitN := 0
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		date := base.Add(time.Duration(commitN) * 36 * time.Hour).Format(time.RFC3339)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
			"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date,
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(msg string) {
		t.Helper()
		git("add", "-A")
		git("commit", "-q", "-m", msg)
		commitN++
	}

	git("init", "-q", "-b", "main")

	write("go.mod", "module example.com/fixture\n\ngo 1.22\n")
	write("db/store.go", `package db

// Store keeps records.
type Store struct{ rows map[string]string }

// NewStore returns an empty store.
func NewStore() *Store { return &Store{rows: map[string]string{}} }

// Get returns a record.
func (s *Store) Get(k string) string { return s.rows[k] }
`)
	write("svc/service.go", `package svc

import "example.com/fixture/db"

// Service wraps the store.
type Service struct{ store *db.Store }

// New returns a service.
func New() *Service { return &Service{store: db.NewStore()} }

// Lookup returns a record.
func (s *Service) Lookup(k string) string { return s.store.Get(k) }
`)
	write("ui/handler.go", `package ui

import "example.com/fixture/db"

// Handle reads straight from the store, skipping the service layer.
func Handle(k string) string { return db.NewStore().Get(k) }
`)
	write("web/db/model.ts", `export interface Model { id: string }

export function loadModel(id: string): Model {
  return { id };
}
`)
	write("web/svc/api.ts", `import { loadModel, Model } from "../db/model";

export function fetchModel(id: string): Model {
  return loadModel(id);
}
`)
	write("web/ui/view.ts", `import { loadModel } from "../db/model";

export function render(id: string): string {
  return loadModel(id).id;
}
`)
	write("layers.toml", `[layers]
ui = "ui/, web/ui"
svc = "svc/, web/svc"
db = "db/, web/db"

[allowed_flows]
ui -> svc
svc -> db
`)
	commit("initial layout")

	for i := 1; i <= 5; i++ {
		write("svc/service.go", fmt.Sprintf(`package svc

import "example.com/fixture/db"

// Service wraps the store.
type Service struct{ store *db.Store }

// New returns a service.
func New() *Service { return &Service{store: db.NewStore()} }

// Lookup returns a record.
func (s *Service) Lookup(k string) string { return s.store.Get(k) }

// Version%d is bumped together with web/svc/api.ts.
func Version%d() int { return %d }
`, i, i, i))
		write("web/svc/api.ts", fmt.Sprintf(`import { loadModel, Model } from "../db/model";

export function fetchModel(id: string): Model {
  return loadModel(id);
}

export const apiVersion%d = %d;
`, i, i))
		// A different extra partner per commit, so api.ts has many partners.
		write(fmt.Sprintf("notes/n%d.ts", i), fmt.Sprintf("export const note%d = %d;\n", i, i))
		commit(fmt.Sprintf("bump api %d", i))
	}

	for i := 1; i <= 3; i++ {
		write("ui/handler.go", fmt.Sprintf(`package ui

import "example.com/fixture/db"

// Handle reads straight from the store, skipping the service layer.
func Handle(k string) string { return db.NewStore().Get(k) }

// Rev%d marks a revision.
func Rev%d() int { return %d }
`, i, i, i))
		write("db/store.go", fmt.Sprintf(`package db

// Store keeps records.
type Store struct{ rows map[string]string }

// NewStore returns an empty store.
func NewStore() *Store { return &Store{rows: map[string]string{}} }

// Get returns a record.
func (s *Store) Get(k string) string { return s.rows[k] }

// Rev%d marks a revision.
func Rev%d() int { return %d }
`, i, i, i))
		commit(fmt.Sprintf("store rev %d", i))
	}
	return root
}

func TestFFIContract(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := fixtureRepo(t)
	if v, err := Version(); err != nil || v == "" {
		t.Errorf("Version() = %q, %v", v, err)
	} else {
		t.Logf("cartographer lib %s (fixture %s)", v, root)
	}

	t.Run("MapProject", func(t *testing.T) {
		g, err := MapProject(root)
		if err != nil {
			t.Fatalf("MapProject: %v", err)
		}
		if len(g.Nodes) == 0 {
			t.Fatal("no nodes")
		}
		for _, n := range g.Nodes {
			if n.ModuleID == "" {
				t.Errorf("node %q: ModuleID empty (snake_case module_id?)", n.Path)
			}
			if n.IsBridge == nil {
				t.Errorf("node %q: IsBridge nil (snake_case is_bridge?)", n.Path)
			}
		}
		if g.Metadata.TotalFiles == 0 {
			t.Error("Metadata.TotalFiles = 0 (snake_case total_files?)")
		}
		if g.Metadata.HealthScore == nil {
			t.Error("Metadata.HealthScore nil (snake_case health_score?)")
		}
		if g.GodModules == nil {
			t.Error("GodModules nil, want a (possibly empty) slice (snake_case god_modules?)")
		}
		if len(g.Edges) == 0 {
			t.Error("no edges: neither the Go nor the TS imports resolved")
		}
		for _, e := range g.Edges {
			if e.Source == "" || e.Target == "" {
				t.Errorf("edge with empty endpoint: %+v", e)
			}
		}
	})

	t.Run("Health", func(t *testing.T) {
		h, err := Health(root)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if h.TotalFiles == 0 {
			t.Error("TotalFiles = 0")
		}
		if h.HealthScore == 0 {
			t.Error("HealthScore = 0")
		}
	})

	t.Run("CheckLayers", func(t *testing.T) {
		vs, err := CheckLayers(root, filepath.Join(root, "layers.toml"))
		if err != nil {
			t.Fatalf("CheckLayers: %v", err)
		}
		if len(vs) == 0 {
			t.Fatal("no violations; ui/handler.go -> db/store.go and web/ui/view.ts -> web/db/model.ts skip svc")
		}
		for _, v := range vs {
			if v.SourcePath == "" || v.TargetPath == "" {
				t.Errorf("violation with empty SourcePath/TargetPath (snake_case source_path?): %+v", v)
			}
			if v.SourceLayer == "" || v.TargetLayer == "" {
				t.Errorf("violation with empty layer names: %+v", v)
			}
		}
	})

	t.Run("SimulateChange", func(t *testing.T) {
		// CKB passes a repo-relative file path as the module ID.
		ia, err := SimulateChange(root, "db/store.go", "", "")
		if err != nil {
			t.Fatalf("SimulateChange: %v", err)
		}
		if ia.TargetModule == "" {
			t.Error("TargetModule empty (snake_case target_module?)")
		}
		if ia.PredictedImpact.RiskLevel == "" {
			t.Error("PredictedImpact.RiskLevel empty (snake_case predicted_impact / risk_level?)")
		}
	})

	t.Run("RankedSkeleton", func(t *testing.T) {
		r, err := RankedSkeleton(root, []string{"db/store.go"}, 2000)
		if err != nil {
			t.Fatalf("RankedSkeleton: %v", err)
		}
		if len(r.Files) == 0 {
			t.Fatal("no files")
		}
		if r.Files[0].Path == "" || r.Files[0].Rank == 0 {
			t.Errorf("first file has empty Path or zero Rank: %+v", r.Files[0])
		}
		for _, f := range r.Files {
			if filepath.IsAbs(f.Path) {
				t.Errorf("ranked skeleton path is absolute: %s", f.Path)
				break
			}
		}
	})

	t.Run("GitChurn", func(t *testing.T) {
		churn, err := GitChurn(root, 0)
		if err != nil {
			t.Fatalf("GitChurn: %v", err)
		}
		if churn["svc/service.go"] < 5 {
			t.Errorf("churn[svc/service.go] = %d, want >= 5 (map: %v)", churn["svc/service.go"], churn)
		}
	})

	checkPairs := func(t *testing.T, pairs []CoChangePair) {
		t.Helper()
		found := false
		for _, p := range pairs {
			if p.FileA == "" || p.FileB == "" || p.Count == 0 || p.CouplingScore == 0 {
				t.Errorf("pair with empty field: %+v", p)
			}
			if pairHas(p, "svc/service.go", "web/svc/api.ts") {
				found = true
				if p.Count < 5 {
					t.Errorf("svc/service.go <-> web/svc/api.ts Count = %d, want >= 5", p.Count)
				}
			}
		}
		if !found {
			t.Errorf("svc/service.go <-> web/svc/api.ts missing from %d pairs", len(pairs))
		}
	}

	t.Run("GitCochange", func(t *testing.T) {
		pairs, err := GitCochange(root, 0, 3)
		if err != nil {
			t.Fatalf("GitCochange: %v", err)
		}
		checkPairs(t, pairs)
	})

	t.Run("HiddenCoupling", func(t *testing.T) {
		pairs, err := HiddenCoupling(root, 0, 3)
		if err != nil {
			t.Fatalf("HiddenCoupling: %v", err)
		}
		checkPairs(t, pairs)
		for _, p := range pairs {
			// ui/handler.go imports db/store.go, so the pair is not hidden.
			if pairHas(p, "ui/handler.go", "db/store.go") {
				t.Errorf("ui/handler.go <-> db/store.go reported hidden, but handler.go imports the db package")
			}
		}
	})

	t.Run("Semidiff", func(t *testing.T) {
		files, err := Semidiff(root, "HEAD~1", "HEAD")
		if err != nil {
			t.Fatalf("Semidiff: %v", err)
		}
		if len(files) == 0 {
			t.Fatal("no files")
		}
		for _, f := range files {
			if f.Path == "" || f.Status == "" {
				t.Errorf("file with empty Path/Status: %+v", f)
			}
		}
	})

	t.Run("UnreferencedSymbols", func(t *testing.T) {
		r, err := UnreferencedSymbols(root)
		if err != nil {
			t.Fatalf("UnreferencedSymbols: %v", err)
		}
		if r.TotalCount == 0 || len(r.Files) == 0 {
			// Rev1..Rev3 and Version1..Version5 are referenced nowhere.
			t.Errorf("TotalCount=%d Files=%d, want the unused Rev/Version funcs", r.TotalCount, len(r.Files))
		}
	})

	t.Run("GetModuleContext", func(t *testing.T) {
		mc, err := GetModuleContext(root, "svc/service.go", 1)
		if err != nil {
			t.Fatalf("GetModuleContext: %v", err)
		}
		if mc.Module.Path == "" || len(mc.Module.Signatures) == 0 {
			t.Errorf("Module = %+v", mc.Module)
		}
	})

	t.Run("QueryContext", func(t *testing.T) {
		r, err := QueryContext(root, "store lookup", nil)
		if err != nil {
			t.Fatalf("QueryContext: %v", err)
		}
		if r.Context == "" || len(r.FilesUsed) == 0 || r.TotalTokens == 0 {
			t.Errorf("Context=%d bytes FilesUsed=%d TotalTokens=%d", len(r.Context), len(r.FilesUsed), r.TotalTokens)
		}
	})

	t.Run("ContextHealth", func(t *testing.T) {
		r, err := ContextHealth(strings.Repeat("func Lookup(k string) string\n", 20), nil)
		if err != nil {
			t.Fatalf("ContextHealth: %v", err)
		}
		if r.TokenCount == 0 || r.Grade == "" {
			t.Errorf("TokenCount=%d Grade=%q", r.TokenCount, r.Grade)
		}
	})

	t.Run("ShotgunSurgery", func(t *testing.T) {
		entries, err := ShotgunSurgery(root, 0, 2)
		if err != nil {
			t.Fatalf("ShotgunSurgery: %v", err)
		}
		if len(entries) == 0 {
			t.Fatal("no entries; web/svc/api.ts changed with 6 distinct partners")
		}
		for _, e := range entries {
			if e.File == "" || e.PartnerCount == 0 || e.TotalCochanges == 0 {
				t.Errorf("entry with empty field (snake_case partner_count?): %+v", e)
			}
		}
	})

	t.Run("Evolution", func(t *testing.T) {
		r, err := Evolution(root, 90)
		if err != nil {
			t.Fatalf("Evolution: %v", err)
		}
		if len(r.Snapshots) == 0 {
			t.Fatal("no snapshots")
		}
		for _, s := range r.Snapshots {
			if s.HealthScore == 0 {
				t.Errorf("snapshot with HealthScore 0 (snake_case health_score?): %+v", s)
			}
		}
		if r.HealthTrend == "" {
			t.Error("HealthTrend empty")
		}
	})

	t.Run("BlastRadius", func(t *testing.T) {
		r, err := BlastRadius(root, "db/store.go", 10)
		if err != nil {
			t.Fatalf("BlastRadius: %v", err)
		}
		if r.ModuleID == "" {
			t.Error("ModuleID empty")
		}
		if len(r.Related) == 0 {
			t.Error("no related modules; svc/service.go and ui/handler.go import db")
		}
	})

	t.Run("RenderArchitecture", func(t *testing.T) {
		r, err := RenderArchitecture(root, "mermaid", "", 0, 0)
		if err != nil {
			t.Fatalf("RenderArchitecture: %v", err)
		}
		if r.Diagram == "" || r.NodeCount == 0 {
			t.Errorf("Diagram=%d bytes NodeCount=%d", len(r.Diagram), r.NodeCount)
		}
	})
}

func pairHas(p CoChangePair, a, b string) bool {
	return (strings.HasSuffix(p.FileA, a) && strings.HasSuffix(p.FileB, b)) ||
		(strings.HasSuffix(p.FileA, b) && strings.HasSuffix(p.FileB, a))
}
