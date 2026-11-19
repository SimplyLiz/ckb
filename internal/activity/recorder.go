package activity

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// bufferSize is the capacity of the recorder's internal channel. Entries
// beyond this are dropped (with a debug log) rather than blocking the
// caller — the ledger must never add latency to a tool call.
const bufferSize = 1024

// flushInterval is how often the writer goroutine flushes buffered rows to
// SQLite even if the buffer isn't full.
const flushInterval = 2 * time.Second

// Config controls activity recorder behavior. It mirrors config.ActivityConfig
// but lives in this package to avoid an import cycle with internal/config.
type Config struct {
	Enabled     bool
	StoreParams bool
}

// Entry describes one completed MCP tool call, ready to be recorded.
type Entry struct {
	Tool          string
	Params        map[string]interface{} // raw tool arguments (nil is fine)
	SessionID     string
	Consumer      string
	StartTime     time.Time // call start, used for ts
	DurationMs    int64
	ResponseBytes int
	Truncated     bool
	Error         string      // empty if the call succeeded
	Data          interface{} // envelope Data field, used for facts extraction
}

// Recorder buffers tool_calls rows and flushes them to SQLite from a single
// writer goroutine, so recording a call never blocks the MCP request path.
// Errors talking to SQLite are logged at debug level and never surfaced.
type Recorder struct {
	cfg    Config
	db     *storage.DB
	logger *slog.Logger

	ch       chan storage.ToolCall
	done     chan struct{}
	closeOne sync.Once
	wg       sync.WaitGroup
}

// NewRecorder creates a Recorder writing to db and starts its writer
// goroutine. If db is nil or cfg.Enabled is false, Record and Close are
// safe no-ops (callers don't need to nil-check before use, including on a
// nil *Recorder).
func NewRecorder(db *storage.DB, cfg Config, logger *slog.Logger) *Recorder {
	if logger == nil {
		logger = slog.Default()
	}
	if db == nil || !cfg.Enabled {
		return &Recorder{cfg: cfg, logger: logger}
	}

	r := &Recorder{
		cfg:    cfg,
		db:     db,
		logger: logger,
		ch:     make(chan storage.ToolCall, bufferSize),
		done:   make(chan struct{}),
	}
	r.wg.Add(1)
	go r.run()
	return r
}

// Record builds a tool_calls row from entry and enqueues it for the writer
// goroutine. Non-blocking: if the buffer is full, the entry is dropped and
// logged at debug level. Safe to call on a nil Recorder or a disabled one.
func (r *Recorder) Record(entry Entry) {
	if r == nil || r.db == nil || !r.cfg.Enabled {
		return
	}

	row := r.buildRow(entry)

	select {
	case r.ch <- row:
	default:
		r.logger.Debug("activity ledger buffer full, dropping entry", "tool", entry.Tool)
	}
}

// buildRow computes hash/params/target/facts for entry. All of this is
// in-memory CPU work (no I/O), so doing it on the caller's goroutine adds
// negligible latency compared to the JSON marshal handleCallTool already does.
func (r *Recorder) buildRow(entry Entry) storage.ToolCall {
	row := storage.ToolCall{
		Ts:            entry.StartTime.UnixMilli(),
		SessionID:     entry.SessionID,
		Consumer:      entry.Consumer,
		Tool:          entry.Tool,
		Target:        extractTarget(entry.Params),
		DurationMs:    entry.DurationMs,
		ResponseBytes: int64(entry.ResponseBytes),
		Truncated:     entry.Truncated,
		Error:         entry.Error,
	}

	row.ParamsHash = hashParams(entry.Params)

	if r.cfg.StoreParams {
		if canonical, err := canonicalParamsJSON(entry.Params); err == nil {
			row.Params = truncateBytes(string(canonical), maxStoredParamsBytes)
		}
	}

	if facts := ExtractFacts(entry.Tool, entry.Data); len(facts) > 0 {
		if b, err := json.Marshal(facts); err == nil {
			row.Facts = string(b)
		}
	}

	return row
}

// Close flushes any buffered rows and stops the writer goroutine. Safe to
// call multiple times and on a nil or disabled Recorder.
func (r *Recorder) Close() {
	if r == nil || r.db == nil || !r.cfg.Enabled {
		return
	}
	r.closeOne.Do(func() {
		close(r.done)
	})
	r.wg.Wait()
}

func (r *Recorder) run() {
	defer r.wg.Done()

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	var buf []storage.ToolCall

	flush := func() {
		if len(buf) == 0 {
			return
		}
		if err := r.db.InsertToolCalls(buf); err != nil {
			r.logger.Debug("activity ledger flush failed", "error", err.Error())
		}
		buf = buf[:0]
	}

	for {
		select {
		case row := <-r.ch:
			buf = append(buf, row)
		case <-ticker.C:
			flush()
		case <-r.done:
			// Drain whatever is already queued, then flush and exit.
			for {
				select {
				case row := <-r.ch:
					buf = append(buf, row)
				default:
					flush()
					return
				}
			}
		}
	}
}
