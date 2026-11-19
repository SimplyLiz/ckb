package query

import (
	"context"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/testutil"
)

// Simulates the 9.3 -> 9.4 upgrade: the FTS content on disk was written by the
// old release (a Go method stored as kind "function", no stamp). The first
// query after upgrading must already see the corrected kind, without the user
// re-indexing or deleting .ckb.
func TestEnsureFTSCurrent_RebuildsContentFromOlderRelease(t *testing.T) {
	fixture := testutil.LoadFixture(t, "go")
	engine, cleanup := setupGoldenEngine(t, fixture)
	defer cleanup()
	ctx := context.Background()
	db := engine.db.Conn()

	var id, name string
	if err := db.QueryRow(`SELECT id, name FROM symbols_fts_content WHERE kind = 'method' LIMIT 1`).Scan(&id, &name); err != nil {
		t.Skipf("fixture has no method symbol: %v", err)
	}

	// Rewind to what a 9.3 install has on disk.
	if _, err := db.Exec(`UPDATE symbols_fts_content SET kind = 'function' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS symbols_fts_meta`); err != nil {
		t.Fatal(err)
	}

	res, err := engine.SearchSymbolsFTS(ctx, name, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range res {
		if r.ID == id {
			found = true
			if r.Kind != "method" {
				t.Errorf("stale kind survived the upgrade: %q, want method", r.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("symbol %s not returned", id)
	}

	// Current content is left alone: a second call must not rebuild.
	if _, err := db.Exec(`UPDATE symbols_fts_content SET kind = 'marker' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureFTSCurrent(ctx); err != nil {
		t.Fatal(err)
	}
	var kind string
	_ = db.QueryRow(`SELECT kind FROM symbols_fts_content WHERE id = ?`, id).Scan(&kind)
	if kind != "marker" {
		t.Errorf("EnsureFTSCurrent rebuilt up-to-date content (kind=%q)", kind)
	}
}

// The query cache lives in the database and is keyed by query text, so
// answers computed by the older release must be dropped together with the FTS
// content, before the rebuild finishes.
func TestInvalidateStaleCaches_DropsOlderReleaseAnswers(t *testing.T) {
	fixture := testutil.LoadFixture(t, "go")
	engine, cleanup := setupGoldenEngine(t, fixture)
	defer cleanup()
	ctx := context.Background()

	if err := engine.cache.SetQueryCache("search:old", `{"stale":true}`, "head", "state", 3600); err != nil {
		t.Fatal(err)
	}
	engine.invalidateStaleCaches(ctx) // content is current: cache survives
	if _, ok, _ := engine.cache.GetQueryCache("search:old", "head"); !ok {
		t.Fatal("cache cleared although FTS content is current")
	}

	if _, err := engine.db.Conn().Exec(`DROP TABLE symbols_fts_meta`); err != nil {
		t.Fatal(err)
	}
	engine.invalidateStaleCaches(ctx)
	if _, ok, _ := engine.cache.GetQueryCache("search:old", "head"); ok {
		t.Error("answer from the older release survived the upgrade")
	}
}
