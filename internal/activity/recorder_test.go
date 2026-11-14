package activity

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "ckb-activity-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := storage.Open(tmpDir, logger)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecorderRecordsAndFlushesOnClose(t *testing.T) {
	db := newTestDB(t)
	r := NewRecorder(db, Config{Enabled: true, StoreParams: true}, testLogger())

	r.Record(Entry{
		Tool:          "prepareChange",
		Params:        map[string]interface{}{"target": "auth/session.go"},
		SessionID:     "sess-1",
		Consumer:      "claude-code",
		StartTime:     time.Now(),
		DurationMs:    123,
		ResponseBytes: 4096,
	})

	r.Close()

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call recorded, got %d", len(calls))
	}
	if calls[0].Tool != "prepareChange" || calls[0].Target != "auth/session.go" {
		t.Errorf("unexpected row: %+v", calls[0])
	}
	if calls[0].ParamsHash == "" {
		t.Errorf("expected params hash to be set")
	}
	if calls[0].Params == "" {
		t.Errorf("expected params to be stored when storeParams=true")
	}
}

func TestRecorderStoreParamsFalseOmitsParams(t *testing.T) {
	db := newTestDB(t)
	r := NewRecorder(db, Config{Enabled: true, StoreParams: false}, testLogger())

	r.Record(Entry{
		Tool:       "searchSymbols",
		Params:     map[string]interface{}{"query": "Engine"},
		StartTime:  time.Now(),
		DurationMs: 10,
	})
	r.Close()

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Params != "" {
		t.Errorf("expected params to be empty when storeParams=false, got %q", calls[0].Params)
	}
	if calls[0].ParamsHash == "" {
		t.Errorf("expected params hash even when storeParams=false")
	}
	if calls[0].Target != "Engine" {
		t.Errorf("expected target still extracted when storeParams=false, got %q", calls[0].Target)
	}
}

func TestRecorderFlushesOnTicker(t *testing.T) {
	db := newTestDB(t)
	r := NewRecorder(db, Config{Enabled: true, StoreParams: true}, testLogger())
	defer r.Close()

	r.Record(Entry{Tool: "findReferences", StartTime: time.Now(), DurationMs: 5})

	// Don't call Close; wait past the flush ticker instead.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		calls, err := db.ListToolCalls(storage.ToolCallFilter{Limit: 10})
		if err != nil {
			t.Fatalf("ListToolCalls failed: %v", err)
		}
		if len(calls) == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected ticker to flush recorded entry within deadline")
}

func TestRecorderDropsWhenBufferFull(t *testing.T) {
	db := newTestDB(t)
	// Use a disabled-looking recorder's internals directly to control the
	// channel size for this test: build one via NewRecorder then fill its
	// channel manually is not possible (unexported), so instead we record
	// far more than bufferSize as fast as possible and assert we survive
	// without blocking and without losing the ability to record afterward.
	r := NewRecorder(db, Config{Enabled: true, StoreParams: true}, testLogger())

	// Fire way more entries than the buffer can hold, without ever letting
	// the writer goroutine drain (best effort: no sleep). This should not
	// block regardless of how many get dropped.
	done := make(chan struct{})
	go func() {
		for i := 0; i < bufferSize*4; i++ {
			r.Record(Entry{Tool: "searchSymbols", StartTime: time.Now(), DurationMs: 1})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("Record blocked instead of dropping when buffer was full")
	}

	r.Close()

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Limit: bufferSize * 5})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	// We can't assert an exact count (depends on scheduler timing), but we
	// must have recorded at least one and never exceeded what was sent.
	if len(calls) == 0 {
		t.Errorf("expected at least some calls recorded")
	}
	if len(calls) > bufferSize*4 {
		t.Errorf("recorded more calls than were sent: %d", len(calls))
	}
}

func TestRecorderDisabledIsNoop(t *testing.T) {
	db := newTestDB(t)
	r := NewRecorder(db, Config{Enabled: false}, testLogger())

	r.Record(Entry{Tool: "prepareChange", StartTime: time.Now()})
	r.Close()

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected no calls recorded when disabled, got %d", len(calls))
	}
}

func TestRecorderNilDBIsNoop(t *testing.T) {
	r := NewRecorder(nil, Config{Enabled: true}, testLogger())
	// Must not panic.
	r.Record(Entry{Tool: "prepareChange", StartTime: time.Now()})
	r.Close()
}

func TestRecorderNilReceiverIsNoop(t *testing.T) {
	var r *Recorder
	// Must not panic on a nil *Recorder.
	r.Record(Entry{Tool: "prepareChange", StartTime: time.Now()})
	r.Close()
}

func TestRecorderCloseIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	r := NewRecorder(db, Config{Enabled: true}, testLogger())
	r.Record(Entry{Tool: "prepareChange", StartTime: time.Now()})
	r.Close()
	// Second close must not panic or hang.
	r.Close()
}

func TestRecorderRecordsErrorAndFacts(t *testing.T) {
	db := newTestDB(t)
	r := NewRecorder(db, Config{Enabled: true, StoreParams: true}, testLogger())

	r.Record(Entry{
		Tool:      "findReferences",
		Params:    map[string]interface{}{"symbolId": "sym-1"},
		StartTime: time.Now(),
		Error:     "symbol not found",
	})
	r.Record(Entry{
		Tool:      "findReferences",
		Params:    map[string]interface{}{"symbolId": "sym-2"},
		StartTime: time.Now(),
		Data:      map[string]interface{}{"references": []map[string]interface{}{{"kind": "call"}}, "totalCount": 1},
	})
	r.Close()

	calls, err := db.ListToolCalls(storage.ToolCallFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}

	var errCall, factsCall *storage.ToolCall
	for i := range calls {
		if calls[i].Error != "" {
			errCall = &calls[i]
		}
		if calls[i].Facts != "" {
			factsCall = &calls[i]
		}
	}
	if errCall == nil || errCall.Error != "symbol not found" {
		t.Errorf("expected error row preserved, got %+v", calls)
	}
	if factsCall == nil {
		t.Errorf("expected a row with facts populated, got %+v", calls)
	}
}
