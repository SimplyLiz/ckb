package lip

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// IndexChangedEvent carries the payload of a LIP `index_changed` push frame.
// The daemon emits one per upsert with the total indexed-file count and the
// URIs that were touched. `Pending` is populated opportunistically from the
// piggybacked IndexStatus probe that keeps the push channel flushed — it may
// be stale by a ping interval but is good enough for cache invalidation.
type IndexChangedEvent struct {
	IndexedFiles int
	AffectedURIs []string
}

// HealthEvent carries the index-health view fetched on each keepalive ping.
// It is the push-equivalent of polling `IndexStatus`: the subscriber pulls
// it once per ping interval instead of every query, and callers key off
// `MixedModels` for the rerank gate.
type HealthEvent struct {
	Available     bool
	MixedModels   bool
	IndexedFiles  int
	ModelsInIndex []string
}

// SubscribeHandler receives push events.
// Implementations must be non-blocking — work should be dispatched to a
// separate goroutine or written to a buffered channel.
type SubscribeHandler interface {
	OnIndexChanged(IndexChangedEvent)
	OnHealth(HealthEvent)
}

// pingInterval is how often the subscriber issues a cheap request to flush
// the daemon's broadcast channel. The LIP daemon drains pending push frames
// only after writing a response, so this caps the worst-case push latency.
// Picked to give ~3 s freshness without generating meaningful daemon load.
const pingInterval = 3 * time.Second

// reconnectMaxBackoff bounds the reconnect backoff so a long outage still
// recovers within ~minute once the daemon returns.
const reconnectMaxBackoff = 30 * time.Second

// Subscribe opens a long-lived connection to the LIP daemon, polls
// `IndexStatus` on a short ticker as a keepalive, and dispatches every
// `index_changed` frame the daemon pushes in between. It reconnects with
// exponential backoff when the daemon is unavailable and returns when ctx
// is cancelled.
//
// The daemon drains queued push frames only after writing a reply to a
// client request (see `session.rs` run loop), so a purely passive reader
// would never observe pushes. The ticker doubles as a health probe: every
// response carries the current `IndexStatusInfo`, so callers get a
// `HealthEvent` per interval for free — replacing the per-query
// `IndexStatus` RPC the engine used to issue.
func Subscribe(ctx context.Context, h SubscribeHandler) {
	backoff := 500 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return
		}
		err := runSession(ctx, h)
		if ctx.Err() != nil {
			return
		}
		// Emit a final "unavailable" health event so consumers fail closed
		// quickly when the daemon drops.
		h.OnHealth(HealthEvent{Available: false})
		if err == nil {
			backoff = 500 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > reconnectMaxBackoff {
			backoff = reconnectMaxBackoff
		}
	}
}

// runSession holds a single connection open: it writes a keepalive on
// `pingInterval`, reads every incoming frame, and routes them by `type`.
// Returns on any I/O error so the caller can reconnect.
func runSession(ctx context.Context, h SubscribeHandler) error {
	conn, err := net.DialTimeout("unix", SocketPath(), 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Writer goroutine: pings until the session is torn down.
	var writeMu sync.Mutex
	ping := func() error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeFrame(conn, map[string]any{"type": "index_status"})
	}
	if err = ping(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-sessCtx.Done():
				return
			case <-t.C:
				if pingErr := ping(); pingErr != nil {
					cancel()
					return
				}
			}
		}
	})

	// Reader loop: any I/O error tears down the session.
	for {
		if sessCtx.Err() != nil {
			break
		}
		frame, readErr := readFrame(conn)
		if readErr != nil {
			cancel()
			wg.Wait()
			return readErr
		}
		dispatchFrame(frame, h)
	}
	wg.Wait()
	return nil
}

// dispatchFrame routes a decoded server frame to the handler by `type` tag.
// The LIP wire format nests ServerMessage inside a `ServerResponse` envelope
// with `ok`/`error` fields.
func dispatchFrame(frame map[string]json.RawMessage, h SubscribeHandler) {
	// Drop envelope: ServerResponse { ok: ServerMessage, error: Option<String> }
	var inner map[string]json.RawMessage
	if raw, ok := frame["ok"]; ok && len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &inner)
	} else {
		inner = frame
	}
	var kind string
	_ = json.Unmarshal(inner["type"], &kind)

	switch kind {
	case "index_changed":
		var ev struct {
			IndexedFiles int      `json:"indexed_files"`
			AffectedURIs []string `json:"affected_uris"`
		}
		if b, ok := marshalInner(inner); ok {
			_ = json.Unmarshal(b, &ev)
		}
		h.OnIndexChanged(IndexChangedEvent{
			IndexedFiles: ev.IndexedFiles,
			AffectedURIs: ev.AffectedURIs,
		})
	case "index_status":
		var s indexStatusResp
		if b, ok := marshalInner(inner); ok {
			_ = json.Unmarshal(b, &s)
		}
		h.OnHealth(HealthEvent{
			Available:     true,
			MixedModels:   s.MixedModels,
			IndexedFiles:  s.IndexedFiles,
			ModelsInIndex: s.ModelsInIndex,
		})
	}
}

func marshalInner(m map[string]json.RawMessage) ([]byte, bool) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}
	return b, true
}

// writeFrame encodes a single length-prefixed JSON frame.
func writeFrame(conn net.Conn, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame, err := encodeFrame(b)
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err = conn.Write(frame); err != nil {
		return err
	}
	return nil
}

// readFrame reads one length-prefixed JSON frame with no overall deadline —
// the ping loop detects dead connections via write failures instead, so the
// reader can block indefinitely between pushes.
func readFrame(conn net.Conn) (map[string]json.RawMessage, error) {
	_ = conn.SetReadDeadline(time.Time{})
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, err
	}
	respLen := binary.BigEndian.Uint32(lenBuf)
	if respLen == 0 || respLen > maxFrameBytes {
		return nil, errors.New("lip: frame length out of range")
	}
	buf := make([]byte, respLen)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, err
	}
	return out, nil
}
