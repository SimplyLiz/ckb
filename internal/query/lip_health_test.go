package query

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// startLipHealthDaemon launches a test LIP socket that speaks the subscriber
// wire format the way the real daemon does: each connection is handled in a
// read/write loop; a `query_index_status` request gets an
// `index_status_result` reflecting the supplied `mixedModels` flag, any other
// type an `unknown_message`. The returned counter records how many
// request frames were served across all connections, so tests can verify
// ping-driven behaviour. Points LIP_SOCKET at the spawned socket.
func startLipHealthDaemon(t *testing.T, mixedModels bool) *int64 {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"type":                    "index_status_result",
		"indexed_files":           1,
		"pending_embedding_files": 0,
		"last_updated_ms":         nil,
		"mixed_models":            mixedModels,
		"models_in_index":         []string{"model-a"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	unknown, err := json.Marshal(map[string]any{
		"type":      "unknown_message",
		"supported": []string{"query_index_status"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	dir, err := os.MkdirTemp("/tmp", "lip")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	sockPath := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("listen: %v", err)
	}

	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", sockPath)

	var reqs int64
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					var lenBuf [4]byte
					if _, err := io.ReadFull(c, lenBuf[:]); err != nil {
						return
					}
					reqBuf := make([]byte, binary.BigEndian.Uint32(lenBuf[:]))
					if _, err := io.ReadFull(c, reqBuf); err != nil {
						return
					}
					var req struct {
						Type string `json:"type"`
					}
					_ = json.Unmarshal(reqBuf, &req)
					resp := unknown
					if req.Type == "query_index_status" {
						atomic.AddInt64(&reqs, 1)
						resp = payload
					}
					var out [4]byte
					binary.BigEndian.PutUint32(out[:], uint32(len(resp)))
					if _, err := c.Write(out[:]); err != nil {
						return
					}
					if _, err := c.Write(resp); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	t.Cleanup(func() {
		ln.Close()
		os.RemoveAll(dir)
		os.Setenv("LIP_SOCKET", prev)
	})
	return &reqs
}

// waitHealth blocks until the subscriber has populated `lipHealthCheckedAt`
// or the timeout elapses. Returns true when the cache was primed.
func waitHealth(t *testing.T, e *Engine, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		e.lipHealthMu.RLock()
		primed := !e.lipHealthCheckedAt.IsZero()
		e.lipHealthMu.RUnlock()
		if primed {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func newSubscribingEngine(t *testing.T) *Engine {
	t.Helper()
	e := &Engine{}
	e.startLipSubscriber()
	t.Cleanup(func() {
		if e.lipSubCancel != nil {
			e.lipSubCancel()
		}
	})
	return e
}

func TestLipSemanticAvailable_HealthyIndex(t *testing.T) {
	startLipHealthDaemon(t, false)
	e := newSubscribingEngine(t)
	if !waitHealth(t, e, 2*time.Second) {
		t.Fatal("subscriber never observed a health frame")
	}
	if !e.lipSemanticAvailable() {
		t.Fatal("lipSemanticAvailable = false for healthy single-model index, want true")
	}
}

func TestLipSemanticAvailable_MixedModels(t *testing.T) {
	startLipHealthDaemon(t, true)
	e := newSubscribingEngine(t)
	if !waitHealth(t, e, 2*time.Second) {
		t.Fatal("subscriber never observed a health frame")
	}
	if e.lipSemanticAvailable() {
		t.Fatal("lipSemanticAvailable = true while MixedModels is set, want false")
	}
}

func TestLipSemanticAvailable_DaemonDown(t *testing.T) {
	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", "/tmp/ckb-lip-nonexistent.sock")
	t.Cleanup(func() { os.Setenv("LIP_SOCKET", prev) })

	e := newSubscribingEngine(t)
	// Give the subscriber a moment to try-and-fail; it should back off without
	// populating the cache.
	time.Sleep(200 * time.Millisecond)
	if e.lipSemanticAvailable() {
		t.Fatal("lipSemanticAvailable = true with no daemon, want false")
	}
}

func TestLipSubscriber_ReusesSingleConnection(t *testing.T) {
	reqs := startLipHealthDaemon(t, false)
	e := newSubscribingEngine(t)
	if !waitHealth(t, e, 2*time.Second) {
		t.Fatal("subscriber never observed a health frame")
	}
	// Multiple calls to lipSemanticAvailable must NOT drive any new requests
	// — the subscriber owns the connection and the check is lock-free.
	before := atomic.LoadInt64(reqs)
	for i := 0; i < 5; i++ {
		_ = e.lipSemanticAvailable()
	}
	if after := atomic.LoadInt64(reqs); after != before {
		t.Fatalf("lipSemanticAvailable triggered %d extra requests, want 0", after-before)
	}
}

func TestGetDegradationWarnings_LipMixedModels(t *testing.T) {
	startLipHealthDaemon(t, true)
	e := newSubscribingEngine(t)
	if !waitHealth(t, e, 2*time.Second) {
		t.Fatal("subscriber never observed a health frame")
	}

	warnings := e.GetDegradationWarnings()
	var found bool
	for _, w := range warnings {
		if w.Code == "lip_mixed_models" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected lip_mixed_models warning, got %+v", warnings)
	}
}

// startLipProbeDaemon handles the three RPCs probeHandshake issues —
// handshake, register_project_root, query_index_status — and records every
// decoded request. supported_messages is set from the supported param so
// tests can toggle the register_project_root feature gate. Returns a
// snapshot closure that safely reads the recorded requests under the
// daemon's own mutex.
func startLipProbeDaemon(t *testing.T, supported []string) func() []map[string]any {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lip")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	sockPath := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("listen: %v", err)
	}

	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", sockPath)

	var mu sync.Mutex
	reqs := []map[string]any{}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var lenBuf [4]byte
				if _, err := io.ReadFull(c, lenBuf[:]); err != nil {
					return
				}
				reqLen := binary.BigEndian.Uint32(lenBuf[:])
				buf := make([]byte, reqLen)
				if _, err := io.ReadFull(c, buf); err != nil {
					return
				}
				var req map[string]any
				_ = json.Unmarshal(buf, &req)

				mu.Lock()
				reqs = append(reqs, req)
				mu.Unlock()

				var resp any
				switch req["type"] {
				case "handshake":
					resp = map[string]any{
						"daemon_version":     "2.3.1",
						"protocol_version":   3,
						"supported_messages": supported,
					}
				case "register_project_root":
					resp = map[string]any{"accepted": true}
				case "query_index_status":
					resp = map[string]any{
						"indexed_files":           10,
						"pending_embedding_files": 0,
						"mixed_models":            false,
						"models_in_index":         []string{"model-a"},
					}
				default:
					resp = map[string]any{}
				}
				payload, _ := json.Marshal(resp)
				var out [4]byte
				binary.BigEndian.PutUint32(out[:], uint32(len(payload)))
				_, _ = c.Write(out[:])
				_, _ = c.Write(payload)
			}(conn)
		}
	}()

	t.Cleanup(func() {
		ln.Close()
		os.RemoveAll(dir)
		os.Setenv("LIP_SOCKET", prev)
	})

	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		out := make([]map[string]any, len(reqs))
		copy(out, reqs)
		return out
	}
}

func TestProbeHandshake_RegistersProjectRoot(t *testing.T) {
	snapshot := startLipProbeDaemon(t, []string{"register_project_root", "query_index_status"})

	e := &Engine{repoRoot: "/my/repo/root"}
	e.probeHandshake()

	got := snapshot()
	var seenRegister bool
	for _, r := range got {
		if r["type"] == "register_project_root" {
			seenRegister = true
			if r["root"] != "/my/repo/root" {
				t.Errorf("root = %v, want /my/repo/root", r["root"])
			}
		}
	}
	if !seenRegister {
		types := make([]string, len(got))
		for i, r := range got {
			types[i], _ = r["type"].(string)
		}
		t.Errorf("register_project_root not sent; request types: %v", types)
	}
}

// When the daemon doesn't advertise register_project_root, probeHandshake
// must not send it (older daemons reject as UnknownMessage).
func TestProbeHandshake_SkipsRegisterWhenUnsupported(t *testing.T) {
	snapshot := startLipProbeDaemon(t, []string{"query_index_status"})

	e := &Engine{repoRoot: "/my/repo/root"}
	e.probeHandshake()

	for _, r := range snapshot() {
		if r["type"] == "register_project_root" {
			t.Errorf("register_project_root sent despite not being advertised")
		}
	}
}

// Engines without a repoRoot (unusual but possible in test fixtures) must
// still handshake cleanly without trying to register an empty root.
func TestProbeHandshake_SkipsRegisterWhenNoRepoRoot(t *testing.T) {
	snapshot := startLipProbeDaemon(t, []string{"register_project_root", "query_index_status"})

	e := &Engine{repoRoot: ""}
	e.probeHandshake()

	for _, r := range snapshot() {
		if r["type"] == "register_project_root" {
			t.Errorf("register_project_root sent with empty repoRoot")
		}
	}
}

func TestGetDegradationWarnings_NoWarningBeforeFirstProbe(t *testing.T) {
	// No daemon running — subscriber can't prime the cache, so no warning
	// should surface regardless of what MixedModels *would* be.
	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", "/tmp/ckb-lip-nonexistent.sock")
	t.Cleanup(func() { os.Setenv("LIP_SOCKET", prev) })

	e := newSubscribingEngine(t)
	warnings := e.GetDegradationWarnings()
	for _, w := range warnings {
		if w.Code == "lip_mixed_models" {
			t.Fatalf("lip_mixed_models warning surfaced before first probe: %+v", w)
		}
	}
}
