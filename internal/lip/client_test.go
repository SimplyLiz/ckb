package lip

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Test daemon
// =============================================================================

// testDaemon is a single-connection Unix socket server that accepts one request,
// validates it, and writes a canned response. It records the decoded request body
// so tests can assert field names and values.
type testDaemon struct {
	t        *testing.T
	ln       net.Listener
	response []byte // raw JSON to send back (without length prefix)
	// populated after a connection is handled
	mu      sync.Mutex
	gotReq  map[string]any
	handled chan struct{}
}

// newTestDaemon starts a test socket server and points LIP_SOCKET at it.
// Calls t.Cleanup to restore the env var and stop the server.
func newTestDaemon(t *testing.T, response any) *testDaemon {
	t.Helper()
	respBytes, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal test response: %v", err)
	}

	// Use os.MkdirTemp instead of t.TempDir: the latter embeds the full test
	// name in the path, easily exceeding macOS's 104-char sun_path limit.
	dir, err := os.MkdirTemp("/tmp", "lip")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	sockPath := filepath.Join(dir, "s.sock") // keep filename short
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("listen: %v", err)
	}

	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", sockPath)

	d := &testDaemon{
		t:        t,
		ln:       ln,
		response: respBytes,
		handled:  make(chan struct{}),
	}

	go d.serve()

	t.Cleanup(func() {
		ln.Close()
		os.RemoveAll(dir)
		os.Setenv("LIP_SOCKET", prev)
	})
	return d
}

func (d *testDaemon) serve() {
	conn, err := d.ln.Accept()
	if err != nil {
		return // listener closed by cleanup
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Read length-prefixed request.
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		d.t.Errorf("testDaemon: read length: %v", err)
		return
	}
	reqLen := binary.BigEndian.Uint32(lenBuf[:])
	reqBuf := make([]byte, reqLen)
	if _, err := io.ReadFull(conn, reqBuf); err != nil {
		d.t.Errorf("testDaemon: read body: %v", err)
		return
	}

	var req map[string]any
	if err := json.Unmarshal(reqBuf, &req); err != nil {
		d.t.Errorf("testDaemon: unmarshal request: %v", err)
		return
	}
	d.mu.Lock()
	d.gotReq = req
	d.mu.Unlock()

	// Write length-prefixed response.
	buf := make([]byte, 4+len(d.response))
	binary.BigEndian.PutUint32(buf, uint32(len(d.response)))
	copy(buf[4:], d.response)
	conn.Write(buf) //nolint:errcheck

	close(d.handled)
}

// waitHandled blocks until the daemon has handled a connection (or 2s timeout).
func (d *testDaemon) waitHandled(t *testing.T) {
	t.Helper()
	select {
	case <-d.handled:
	case <-time.After(2 * time.Second):
		t.Fatal("testDaemon: timed out waiting for connection")
	}
}

// req returns the decoded request map (after waitHandled).
func (d *testDaemon) req() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.gotReq
}

// =============================================================================
// Helper — assert field
// =============================================================================

func assertField(t *testing.T, m map[string]any, key string, want any) {
	t.Helper()
	got, ok := m[key]
	if !ok {
		t.Errorf("request missing field %q", key)
		return
	}
	// JSON numbers decode as float64; compare via JSON round-trip for simplicity.
	gotJ, _ := json.Marshal(got)
	wantJ, _ := json.Marshal(want)
	if string(gotJ) != string(wantJ) {
		t.Errorf("field %q = %s, want %s", key, gotJ, wantJ)
	}
}

func assertNoField(t *testing.T, m map[string]any, key string) {
	t.Helper()
	if _, ok := m[key]; ok {
		t.Errorf("request should not contain field %q", key)
	}
}

// =============================================================================
// Degradation
// =============================================================================

// TestDegradation_NoSocket verifies every public function returns nil/zero when
// the socket does not exist — not an error, just nil.
func TestDegradation_NoSocket(t *testing.T) {
	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", "/tmp/lip-nonexistent-ckb-test.sock")
	defer os.Setenv("LIP_SOCKET", prev)

	t.Run("IndexStatus", func(t *testing.T) {
		r, err := IndexStatus()
		if r != nil || err != nil {
			t.Errorf("want (nil,nil), got (%v,%v)", r, err)
		}
	})
	t.Run("NearestByText", func(t *testing.T) {
		r, err := NearestByText("query", 5)
		if r != nil || err != nil {
			t.Errorf("want (nil,nil), got (%v,%v)", r, err)
		}
	})
	t.Run("Similarity", func(t *testing.T) {
		score, ok, err := Similarity("a", "b")
		if score != 0 || ok || err != nil {
			t.Errorf("want (0,false,nil), got (%v,%v,%v)", score, ok, err)
		}
	})
	t.Run("GetEmbeddingsBatch", func(t *testing.T) {
		r, err := GetEmbeddingsBatch([]string{"file://a"}, "")
		if r != nil || err != nil {
			t.Errorf("want (nil,nil), got (%v,%v)", r, err)
		}
	})
	t.Run("NoveltyScore", func(t *testing.T) {
		r, err := NoveltyScore([]string{"file://a"})
		if r != nil || err != nil {
			t.Errorf("want (nil,nil), got (%v,%v)", r, err)
		}
	})
	t.Run("SimilarityMatrix", func(t *testing.T) {
		uris, matrix, err := SimilarityMatrix([]string{"a", "b"})
		if uris != nil || matrix != nil || err != nil {
			t.Errorf("want (nil,nil,nil)")
		}
	})
}

// =============================================================================
// Early-exit (no socket needed)
// =============================================================================

func TestEarlyExit_EmptyInputs(t *testing.T) {
	// These must return nil immediately without touching the socket.
	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", "/tmp/lip-nonexistent-ckb-test.sock")
	defer os.Setenv("LIP_SOCKET", prev)

	if r, _ := GetEmbeddingsBatch(nil, ""); r != nil {
		t.Error("empty uris: want nil")
	}
	if r, _ := BatchNearestByText(nil, 5, "", "", 0); r != nil {
		t.Error("empty queries: want nil")
	}
	if r, _ := Outliers(nil, 5); r != nil {
		t.Error("empty uris: want nil")
	}
	if r, _ := NoveltyScore(nil); r != nil {
		t.Error("empty uris: want nil")
	}
	if r, _, _ := GetCentroid(nil); r != nil {
		t.Error("empty uris: want nil")
	}
	if r, _ := Cluster(nil, 0.8); r != nil {
		t.Error("empty uris: want nil")
	}
	if r, _ := ExportEmbeddings(nil); r != nil {
		t.Error("empty uris: want nil")
	}
	if r, _ := BatchAnnotationGet(nil, "k"); r != nil {
		t.Error("empty uris: want nil")
	}
}

func TestEarlyExit_SimilarityMatrix_TooFew(t *testing.T) {
	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", "/tmp/lip-nonexistent-ckb-test.sock")
	defer os.Setenv("LIP_SOCKET", prev)

	uris, matrix, err := SimilarityMatrix([]string{"only-one"})
	if uris != nil || matrix != nil || err != nil {
		t.Error("single URI: want (nil,nil,nil)")
	}
	uris, matrix, err = SimilarityMatrix(nil)
	if uris != nil || matrix != nil || err != nil {
		t.Error("nil URIs: want (nil,nil,nil)")
	}
}

// =============================================================================
// Wire protocol — type field and field names
// =============================================================================

func TestWireProtocol_IndexStatus(t *testing.T) {
	ts := int64(1744848000000) // 2026-04-13T00:00:00Z in ms
	d := newTestDaemon(t, indexStatusResp{
		IndexedFiles: 42, Pending: 1, LastUpdatedMs: &ts,
		MixedModels: true, ModelsInIndex: []string{"model-a", "model-b"},
	})

	got, err := IndexStatus()
	d.waitHandled(t)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("got nil result")
	}
	if got.IndexedFiles != 42 {
		t.Errorf("IndexedFiles = %d, want 42", got.IndexedFiles)
	}
	if got.Pending != 1 {
		t.Errorf("Pending = %d, want 1", got.Pending)
	}
	if got.LastUpdated != "2026-04-17T00:00:00Z" {
		// The exact value depends on timezone; just check it's non-empty and RFC3339-ish.
		if got.LastUpdated == "" {
			t.Error("LastUpdated is empty, want RFC3339 string")
		}
	}
	if !got.MixedModels {
		t.Error("MixedModels = false, want true")
	}
	if len(got.ModelsInIndex) != 2 {
		t.Errorf("ModelsInIndex len = %d, want 2", len(got.ModelsInIndex))
	}

	req := d.req()
	assertField(t, req, "type", "query_index_status")
	// No other fields expected.
	if len(req) != 1 {
		t.Errorf("expected exactly 1 field in request, got %d: %v", len(req), req)
	}
}

func TestWireProtocol_FileStatus(t *testing.T) {
	d := newTestDaemon(t, fileStatusResp{Indexed: true, AgeSeconds: 300, EmbeddingModel: "nomic"})

	got, err := FileStatus("file://internal/foo.go")
	d.waitHandled(t)

	if err != nil || got == nil {
		t.Fatalf("unexpected: err=%v, got=%v", err, got)
	}
	if got.EmbeddingModel != "nomic" {
		t.Errorf("EmbeddingModel = %q, want %q", got.EmbeddingModel, "nomic")
	}

	req := d.req()
	assertField(t, req, "type", "query_file_status")
	assertField(t, req, "uri", "file://internal/foo.go")
}

func TestWireProtocol_NearestByTextFiltered_AllFields(t *testing.T) {
	d := newTestDaemon(t, nearestResp{Results: []NearestResult{
		{URI: "file://a.go", Score: 0.9},
	}})

	got, err := NearestByTextFiltered("connection pool", 10, "internal/**", 0.5, "nomic")
	d.waitHandled(t)

	if err != nil || len(got) != 1 {
		t.Fatalf("unexpected: err=%v, len=%d", err, len(got))
	}
	if got[0].URI != "file://a.go" {
		t.Errorf("URI = %q, want file://a.go", got[0].URI)
	}

	req := d.req()
	assertField(t, req, "type", "query_nearest_by_text")
	assertField(t, req, "text", "connection pool")
	assertField(t, req, "top_k", 10)
	assertField(t, req, "filter", "internal/**")
	assertField(t, req, "min_score", 0.5)
	assertField(t, req, "model", "nomic")
}

func TestWireProtocol_NearestByTextFiltered_Omitempty(t *testing.T) {
	// filter, min_score, model are all zero — must NOT appear in the request.
	d := newTestDaemon(t, nearestResp{})

	_, _ = NearestByTextFiltered("query", 5, "", 0, "")
	d.waitHandled(t)

	req := d.req()
	assertField(t, req, "type", "query_nearest_by_text")
	assertNoField(t, req, "filter")
	assertNoField(t, req, "min_score")
	assertNoField(t, req, "model")
}

func TestWireProtocol_NearestByFileFiltered(t *testing.T) {
	d := newTestDaemon(t, nearestResp{Results: []NearestResult{{URI: "file://b_test.go", Score: 0.7}}})

	got, err := NearestByFileFiltered("file://b.go", 5, "*_test.go", 0.6)
	d.waitHandled(t)

	if err != nil || len(got) != 1 {
		t.Fatalf("unexpected: err=%v len=%d", err, len(got))
	}
	req := d.req()
	assertField(t, req, "type", "query_nearest")
	assertField(t, req, "uri", "file://b.go")
	assertField(t, req, "top_k", 5)
	assertField(t, req, "filter", "*_test.go")
	assertField(t, req, "min_score", 0.6)
}

// NearestByFile is a thin wrapper — verify it delegates to NearestByFileFiltered
// with no filter/min_score fields on the wire.
func TestWireProtocol_NearestByFile_Delegates(t *testing.T) {
	d := newTestDaemon(t, nearestResp{})

	_, _ = NearestByFile("file://x.go", 3)
	d.waitHandled(t)

	req := d.req()
	assertField(t, req, "type", "query_nearest")
	assertNoField(t, req, "filter")
	assertNoField(t, req, "min_score")
}

func TestWireProtocol_EmbeddingsBatch(t *testing.T) {
	d := newTestDaemon(t, embeddingBatchResp{Vectors: [][]float32{{0.1, 0.2}, {0.3, 0.4}}})

	got, err := GetEmbeddingsBatch([]string{"file://a.go", "file://b.go"}, "nomic")
	d.waitHandled(t)

	if err != nil || len(got) != 2 {
		t.Fatalf("unexpected: err=%v len=%d", err, len(got))
	}
	req := d.req()
	assertField(t, req, "type", "embedding_batch")
	assertField(t, req, "uris", []string{"file://a.go", "file://b.go"})
	assertField(t, req, "model", "nomic")
}

// GetEmbedding delegates to GetEmbeddingsBatch — verify single-element slice on wire.
func TestWireProtocol_GetEmbedding_Delegates(t *testing.T) {
	d := newTestDaemon(t, embeddingBatchResp{Vectors: [][]float32{{1.0, 2.0}}})

	vec, err := GetEmbedding("file://x.go", "")
	d.waitHandled(t)

	if err != nil || len(vec) != 2 {
		t.Fatalf("unexpected: err=%v len=%d", err, len(vec))
	}
	req := d.req()
	assertField(t, req, "type", "embedding_batch")
	// Single-element uris slice.
	uris, _ := req["uris"].([]any)
	if len(uris) != 1 {
		t.Errorf("expected 1 URI in batch, got %d", len(uris))
	}
	// model="" → omitted.
	assertNoField(t, req, "model")
}

func TestWireProtocol_Similarity_Present(t *testing.T) {
	score := float32(0.85)
	d := newTestDaemon(t, similarityResp{Score: &score})

	got, ok, err := Similarity("file://a.go", "file://b.go")
	d.waitHandled(t)

	if err != nil || !ok {
		t.Fatalf("unexpected: err=%v ok=%v", err, ok)
	}
	if got != 0.85 {
		t.Errorf("score = %v, want 0.85", got)
	}
	req := d.req()
	assertField(t, req, "type", "similarity")
	assertField(t, req, "uri_a", "file://a.go")
	assertField(t, req, "uri_b", "file://b.go")
}

// When the daemon returns a null score, Similarity must return ok=false.
func TestWireProtocol_Similarity_Null(t *testing.T) {
	d := newTestDaemon(t, similarityResp{Score: nil})

	_, ok, err := Similarity("file://a.go", "file://b.go")
	d.waitHandled(t)

	if err != nil || ok {
		t.Errorf("want (_, false, nil), got (_, %v, %v)", ok, err)
	}
}

func TestWireProtocol_SemanticDrift(t *testing.T) {
	dist := float32(0.42)
	d := newTestDaemon(t, semanticDriftResp{Distance: &dist})

	got, ok, err := SemanticDrift("file://a.go", "file://b.go")
	d.waitHandled(t)

	if err != nil || !ok || got != 0.42 {
		t.Errorf("unexpected: err=%v ok=%v got=%v", err, ok, got)
	}
	req := d.req()
	assertField(t, req, "type", "query_semantic_drift")
	assertField(t, req, "uri_a", "file://a.go")
	assertField(t, req, "uri_b", "file://b.go")
}

func TestWireProtocol_NoveltyScore(t *testing.T) {
	d := newTestDaemon(t, noveltyScoreResp{
		Score: 0.73,
		PerFile: []NoveltyItem{
			{URI: "file://new.go", Score: 0.9},
			{URI: "file://old.go", Score: 0.3},
		},
	})

	got, err := NoveltyScore([]string{"file://new.go", "file://old.go"})
	d.waitHandled(t)

	if err != nil || got == nil {
		t.Fatalf("unexpected: err=%v got=%v", err, got)
	}
	if got.Score != 0.73 {
		t.Errorf("Score = %v, want 0.73", got.Score)
	}
	if len(got.PerFile) != 2 {
		t.Errorf("PerFile len = %d, want 2", len(got.PerFile))
	}
	req := d.req()
	assertField(t, req, "type", "query_novelty_score")
	assertField(t, req, "uris", []string{"file://new.go", "file://old.go"})
}

func TestWireProtocol_Coverage(t *testing.T) {
	frac := float32(0.94)
	d := newTestDaemon(t, coverageResp{
		Root: "/repo", TotalFiles: 100, EmbeddedFiles: 94,
		CoverageFraction: &frac,
		ByDirectory:      []DirCoverage{{Path: "internal", TotalFiles: 50, EmbeddedFiles: 47}},
	})

	got, err := Coverage("/repo")
	d.waitHandled(t)

	if err != nil || got == nil {
		t.Fatalf("unexpected: err=%v got=%v", err, got)
	}
	if got.CoverageFraction != 0.94 {
		t.Errorf("CoverageFraction = %v, want 0.94", got.CoverageFraction)
	}
	if len(got.ByDirectory) != 1 {
		t.Errorf("ByDirectory len = %d, want 1", len(got.ByDirectory))
	}
	req := d.req()
	assertField(t, req, "type", "query_coverage")
	assertField(t, req, "root", "/repo")
}

// Coverage with nil CoverageFraction in response must return 0, not panic.
func TestWireProtocol_Coverage_NilFraction(t *testing.T) {
	d := newTestDaemon(t, coverageResp{Root: "/repo", TotalFiles: 10, EmbeddedFiles: 0, CoverageFraction: nil})

	got, err := Coverage("/repo")
	d.waitHandled(t)

	if err != nil || got == nil {
		t.Fatalf("unexpected: err=%v got=%v", err, got)
	}
	if got.CoverageFraction != 0 {
		t.Errorf("CoverageFraction = %v, want 0", got.CoverageFraction)
	}
}

func TestWireProtocol_FindBoundaries(t *testing.T) {
	d := newTestDaemon(t, boundariesResp{Boundaries: []BoundaryRange{
		{StartLine: 1, EndLine: 45, ShiftMagnitude: 0.71, NearestSymbol: "handleAuth"},
		{StartLine: 46, EndLine: 90, ShiftMagnitude: 0.55, NearestSymbol: "renderResponse"},
	}})

	got, err := FindBoundaries("file://handler.go", 30, 0.3, "nomic")
	d.waitHandled(t)

	if err != nil || len(got) != 2 {
		t.Fatalf("unexpected: err=%v len=%d", err, len(got))
	}
	if got[0].NearestSymbol != "handleAuth" {
		t.Errorf("NearestSymbol = %q, want handleAuth", got[0].NearestSymbol)
	}
	req := d.req()
	assertField(t, req, "type", "find_boundaries")
	assertField(t, req, "uri", "file://handler.go")
	assertField(t, req, "chunk_lines", 30)
	assertField(t, req, "threshold", 0.3)
	assertField(t, req, "model", "nomic")
}

// FindBoundaries zero values must be omitted from the wire (daemon uses its defaults).
func TestWireProtocol_FindBoundaries_Omitempty(t *testing.T) {
	d := newTestDaemon(t, boundariesResp{})

	_, _ = FindBoundaries("file://x.go", 0, 0, "")
	d.waitHandled(t)

	req := d.req()
	assertNoField(t, req, "chunk_lines")
	assertNoField(t, req, "threshold")
	assertNoField(t, req, "model")
}

func TestWireProtocol_SimilarityMatrix(t *testing.T) {
	d := newTestDaemon(t, similarityMatrixResp{
		URIs:   []string{"file://a.go", "file://b.go"},
		Matrix: [][]float32{{1.0, 0.8}, {0.8, 1.0}},
	})

	uris, matrix, err := SimilarityMatrix([]string{"file://a.go", "file://b.go"})
	d.waitHandled(t)

	if err != nil || len(uris) != 2 || len(matrix) != 2 {
		t.Fatalf("unexpected: err=%v uris=%v", err, uris)
	}
	if matrix[0][1] != 0.8 {
		t.Errorf("matrix[0][1] = %v, want 0.8", matrix[0][1])
	}
	req := d.req()
	assertField(t, req, "type", "similarity_matrix")
}

func TestWireProtocol_GetCentroid(t *testing.T) {
	d := newTestDaemon(t, centroidResp{Vector: []float32{0.1, 0.2, 0.3}, Included: 5})

	vec, n, err := GetCentroid([]string{"file://a.go", "file://b.go"})
	d.waitHandled(t)

	if err != nil || len(vec) != 3 || n != 5 {
		t.Fatalf("unexpected: err=%v vec=%v n=%d", err, vec, n)
	}
	req := d.req()
	assertField(t, req, "type", "get_centroid")
}

func TestWireProtocol_BatchNearestByText(t *testing.T) {
	d := newTestDaemon(t, batchNearestResp{Results: [][]NearestResult{
		{{URI: "file://a.go", Score: 0.9}},
		{{URI: "file://b.go", Score: 0.7}},
	}})

	got, err := BatchNearestByText([]string{"q1", "q2"}, 5, "internal/**", "nomic", 0.4)
	d.waitHandled(t)

	if err != nil || len(got) != 2 {
		t.Fatalf("unexpected: err=%v len=%d", err, len(got))
	}
	req := d.req()
	assertField(t, req, "type", "batch_query_nearest_by_text")
	assertField(t, req, "queries", []string{"q1", "q2"})
	assertField(t, req, "top_k", 5)
	assertField(t, req, "filter", "internal/**")
	assertField(t, req, "model", "nomic")
	assertField(t, req, "min_score", 0.4)
}

func TestWireProtocol_NearestByContrast(t *testing.T) {
	d := newTestDaemon(t, nearestResp{Results: []NearestResult{{URI: "file://c.go", Score: 0.8}}})

	got, err := NearestByContrast("file://like.go", "file://unlike.go", 3)
	d.waitHandled(t)

	if err != nil || len(got) != 1 {
		t.Fatalf("unexpected: err=%v", err)
	}
	req := d.req()
	assertField(t, req, "type", "query_nearest_by_contrast")
	assertField(t, req, "like_uri", "file://like.go")
	assertField(t, req, "unlike_uri", "file://unlike.go")
	assertField(t, req, "top_k", 3)
}

func TestWireProtocol_GetAnnotation_Found(t *testing.T) {
	val := "stable"
	d := newTestDaemon(t, annotationGetResp{Value: &val})

	v, ok, err := GetAnnotation("lip://sym", "stability")
	d.waitHandled(t)

	if err != nil || !ok || v != "stable" {
		t.Errorf("unexpected: err=%v ok=%v v=%q", err, ok, v)
	}
	req := d.req()
	assertField(t, req, "type", "annotation_get")
	assertField(t, req, "symbol_uri", "lip://sym")
	assertField(t, req, "key", "stability")
}

// When the daemon returns a null value, GetAnnotation must return ok=false.
func TestWireProtocol_GetAnnotation_Absent(t *testing.T) {
	d := newTestDaemon(t, annotationGetResp{Value: nil})

	_, ok, err := GetAnnotation("lip://sym", "stability")
	d.waitHandled(t)

	if err != nil || ok {
		t.Errorf("want (_, false, nil), got ok=%v err=%v", ok, err)
	}
}

func TestWireProtocol_BatchAnnotationGet(t *testing.T) {
	present := "stable"
	d := newTestDaemon(t, batchAnnotationResp{
		Entries: map[string]*string{
			"lip://a": &present,
			"lip://b": nil, // absent — must be filtered from result
		},
	})

	got, err := BatchAnnotationGet([]string{"lip://a", "lip://b"}, "stability")
	d.waitHandled(t)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["lip://a"] != "stable" {
		t.Errorf("lip://a = %q, want stable", got["lip://a"])
	}
	if _, ok := got["lip://b"]; ok {
		t.Error("lip://b should be absent (nil value filtered)")
	}
	req := d.req()
	assertField(t, req, "type", "batch_annotation_get")
	assertField(t, req, "key", "stability")
}

func TestWireProtocol_RegisterProjectRoot_Accepted(t *testing.T) {
	d := newTestDaemon(t, deltaAckResp{Accepted: true})

	ok, err := RegisterProjectRoot("/abs/path/to/repo")
	d.waitHandled(t)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("accepted = false, want true")
	}
	req := d.req()
	assertField(t, req, "type", "register_project_root")
	assertField(t, req, "root", "/abs/path/to/repo")
}

// When the daemon rejects the root (e.g. invalid path), the RPC should return
// (false, nil) — non-fatal, callers fall back to auto-detection.
func TestWireProtocol_RegisterProjectRoot_Rejected(t *testing.T) {
	reason := "invalid root"
	d := newTestDaemon(t, deltaAckResp{Accepted: false, Error: &reason})

	ok, err := RegisterProjectRoot("/bogus")
	d.waitHandled(t)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("accepted = true, want false")
	}
}

// Empty root must short-circuit — no socket call.
func TestWireProtocol_RegisterProjectRoot_EmptyRoot(t *testing.T) {
	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", "/tmp/lip-nonexistent-ckb-test.sock")
	defer os.Setenv("LIP_SOCKET", prev)

	ok, err := RegisterProjectRoot("")
	if ok || err != nil {
		t.Errorf("empty root: want (false, nil), got (%v, %v)", ok, err)
	}
}

// =============================================================================
// Outgoing impact
// =============================================================================

func TestWireProtocol_QueryOutgoingImpact_WithResult(t *testing.T) {
	d := newTestDaemon(t, outgoingImpactResp{Result: &OutgoingImpactEntry{
		TargetURI: "lip://local//repo/foo.go#Caller",
		DirectItems: []BlastRadiusItem{
			{FileURI: "lip://local//repo/bar.go", SymbolURI: "lip://local//repo/bar.go#Callee",
				Distance: 1, Confidence: 0.95},
		},
		TransitiveItems: []BlastRadiusItem{
			{FileURI: "lip://local//repo/baz.go", SymbolURI: "lip://local//repo/baz.go#Deep",
				Distance: 2, Confidence: 0.85},
		},
		SemanticItems: []BlastRadiusSemanticItem{
			{FileURI: "lip://local//repo/similar.go", SymbolURI: "...#Similar",
				Similarity: 0.82, Source: "semantic"},
		},
		EdgesSource: "scip_with_tier1_edges",
		Truncated:   false,
	}})

	got, err := QueryOutgoingImpact("lip://local//repo/foo.go#Caller", 0.6)
	d.waitHandled(t)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("got nil result")
	}
	if got.TargetURI != "lip://local//repo/foo.go#Caller" {
		t.Errorf("TargetURI = %q", got.TargetURI)
	}
	if len(got.DirectItems) != 1 || got.DirectItems[0].Distance != 1 {
		t.Errorf("DirectItems = %+v", got.DirectItems)
	}
	if len(got.TransitiveItems) != 1 || got.TransitiveItems[0].Distance != 2 {
		t.Errorf("TransitiveItems = %+v", got.TransitiveItems)
	}
	if len(got.SemanticItems) != 1 || got.SemanticItems[0].Source != "semantic" {
		t.Errorf("SemanticItems = %+v", got.SemanticItems)
	}
	if got.EdgesSource != "scip_with_tier1_edges" {
		t.Errorf("EdgesSource = %q", got.EdgesSource)
	}

	req := d.req()
	assertField(t, req, "type", "query_outgoing_impact")
	assertField(t, req, "symbol_uri", "lip://local//repo/foo.go#Caller")
	assertField(t, req, "min_score", 0.6)
}

// Null result (target not indexed) must come back as (nil, nil).
func TestWireProtocol_QueryOutgoingImpact_NullResult(t *testing.T) {
	d := newTestDaemon(t, outgoingImpactResp{Result: nil})

	got, err := QueryOutgoingImpact("lip://local//repo/unknown.go#X", 0.6)
	d.waitHandled(t)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("want nil result for unindexed target, got %+v", got)
	}
}

// min_score=0 must be omitted so the daemon applies its default.
func TestWireProtocol_QueryOutgoingImpact_OmitMinScore(t *testing.T) {
	d := newTestDaemon(t, outgoingImpactResp{})

	_, _ = QueryOutgoingImpact("lip://x#X", 0)
	d.waitHandled(t)

	req := d.req()
	assertField(t, req, "type", "query_outgoing_impact")
	assertField(t, req, "symbol_uri", "lip://x#X")
	assertNoField(t, req, "min_score")
}

// Empty symbol URI must short-circuit — no socket call.
func TestWireProtocol_QueryOutgoingImpact_EmptySymbol(t *testing.T) {
	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", "/tmp/lip-nonexistent-ckb-test.sock")
	defer os.Setenv("LIP_SOCKET", prev)

	got, err := QueryOutgoingImpact("", 0.6)
	if got != nil || err != nil {
		t.Errorf("empty symbol: want (nil, nil), got (%v, %v)", got, err)
	}
}

func TestOutgoingEntryToExternal(t *testing.T) {
	entry := &OutgoingImpactEntry{
		TargetURI: "lip://x#X",
		DirectItems: []BlastRadiusItem{
			{FileURI: "f1", SymbolURI: "f1#A", Distance: 1, Confidence: 0.9},
		},
		TransitiveItems: []BlastRadiusItem{
			{FileURI: "f2", SymbolURI: "f2#B", Distance: 2, Confidence: 0.8},
		},
		SemanticItems: []BlastRadiusSemanticItem{
			{FileURI: "f3", SymbolURI: "f3#C", Similarity: 0.75, Source: "both"},
		},
		EdgesSource: "tier1",
	}
	ext := OutgoingEntryToExternal(entry)
	if ext == nil {
		t.Fatal("got nil")
	}
	if len(ext.DirectItems) != 1 || ext.DirectItems[0].SymbolURI != "f1#A" {
		t.Errorf("DirectItems: %+v", ext.DirectItems)
	}
	if len(ext.TransitiveItems) != 1 || ext.TransitiveItems[0].Distance != 2 {
		t.Errorf("TransitiveItems: %+v", ext.TransitiveItems)
	}
	if len(ext.SemanticItems) != 1 || ext.SemanticItems[0].Source != "both" {
		t.Errorf("SemanticItems: %+v", ext.SemanticItems)
	}
	if ext.EdgesSource != "tier1" {
		t.Errorf("EdgesSource = %q", ext.EdgesSource)
	}
	if ext.RiskLevel != "" {
		t.Errorf("RiskLevel should be empty for outgoing, got %q", ext.RiskLevel)
	}
}

func TestOutgoingEntryToExternal_Nil(t *testing.T) {
	if got := OutgoingEntryToExternal(nil); got != nil {
		t.Errorf("nil entry: want nil, got %+v", got)
	}
}

func TestWireProtocol_Handshake(t *testing.T) {
	d := newTestDaemon(t, handshakeResp{DaemonVersion: "2.0.0", ProtocolVersion: 2})

	got, err := Handshake("9.0.0")
	d.waitHandled(t)

	if err != nil || got == nil {
		t.Fatalf("unexpected: err=%v got=%v", err, got)
	}
	if got.DaemonVersion != "2.0.0" || got.ProtocolVersion != 2 {
		t.Errorf("unexpected: %+v", got)
	}
	req := d.req()
	assertField(t, req, "type", "handshake")
	assertField(t, req, "client_version", "9.0.0")
}

// Handshake with empty clientVersion must omit the field from the wire.
func TestWireProtocol_Handshake_NoVersion(t *testing.T) {
	d := newTestDaemon(t, handshakeResp{})

	_, _ = Handshake("")
	d.waitHandled(t)

	req := d.req()
	assertNoField(t, req, "client_version")
}

func TestWireProtocol_ExplainMatch(t *testing.T) {
	d := newTestDaemon(t, explainMatchResp{
		Chunks: []ExplanationChunk{
			{StartLine: 10, EndLine: 40, ChunkText: "func handleAuth", Score: 0.91},
		},
		QueryModel: "nomic",
	})

	chunks, model, err := ExplainMatch("authentication flow", "file://handler.go", 3, 30, "nomic")
	d.waitHandled(t)

	if err != nil || len(chunks) != 1 || model != "nomic" {
		t.Fatalf("unexpected: err=%v len=%d model=%q", err, len(chunks), model)
	}
	if chunks[0].Score != 0.91 {
		t.Errorf("Score = %v, want 0.91", chunks[0].Score)
	}
	req := d.req()
	assertField(t, req, "type", "explain_match")
	assertField(t, req, "query", "authentication flow")
	assertField(t, req, "result_uri", "file://handler.go")
	assertField(t, req, "top_k", 3)
	assertField(t, req, "chunk_lines", 30)
	assertField(t, req, "model", "nomic")
}

func TestWireProtocol_ExplainMatch_Omitempty(t *testing.T) {
	d := newTestDaemon(t, explainMatchResp{})

	_, _, _ = ExplainMatch("q", "file://x.go", 0, 0, "")
	d.waitHandled(t)

	req := d.req()
	assertNoField(t, req, "top_k")
	assertNoField(t, req, "chunk_lines")
	assertNoField(t, req, "model")
}

func TestWireProtocol_NearestBySymbol(t *testing.T) {
	d := newTestDaemon(t, nearestResp{Results: []NearestResult{{URI: "file://x.go", Score: 0.8}}})

	got, err := NearestBySymbol("lip://Engine", 5)
	d.waitHandled(t)

	if err != nil || len(got) != 1 {
		t.Fatalf("unexpected: err=%v", err)
	}
	req := d.req()
	assertField(t, req, "type", "query_nearest_by_symbol")
	assertField(t, req, "symbol_uri", "lip://Engine")
	assertField(t, req, "top_k", 5)
}

func TestWireProtocol_StaleEmbeddings(t *testing.T) {
	d := newTestDaemon(t, staleEmbeddingsResp{URIs: []string{"file://old.go", "file://ancient.go"}})

	got, err := StaleEmbeddings("/repo")
	d.waitHandled(t)

	if err != nil || len(got) != 2 {
		t.Fatalf("unexpected: err=%v len=%d", err, len(got))
	}
	req := d.req()
	assertField(t, req, "type", "query_stale_embeddings")
	assertField(t, req, "root", "/repo")
}

func TestWireProtocol_PruneDeleted(t *testing.T) {
	d := newTestDaemon(t, pruneDeletedResp{Checked: 50, Removed: []string{"file://a.go", "file://b.go"}})

	checked, removed, err := PruneDeleted()
	d.waitHandled(t)

	if err != nil || checked != 50 || removed != 2 {
		t.Fatalf("unexpected: err=%v checked=%d removed=%d", err, checked, removed)
	}
	req := d.req()
	assertField(t, req, "type", "prune_deleted")
}

func TestWireProtocol_QueryExpansion(t *testing.T) {
	d := newTestDaemon(t, queryExpansionResp{Terms: []string{"connection pool", "retry budget"}})

	got, err := QueryExpansion("rate limiter", 5)
	d.waitHandled(t)

	if err != nil || len(got) != 2 {
		t.Fatalf("unexpected: err=%v len=%d", err, len(got))
	}
	req := d.req()
	assertField(t, req, "type", "query_expansion")
	assertField(t, req, "query", "rate limiter")
	assertField(t, req, "top_k", 5)
}

func TestWireProtocol_SemanticDiff(t *testing.T) {
	d := newTestDaemon(t, semanticDiffResp{
		Distance:     0.34,
		MovingToward: []NearestResult{{URI: "file://auth.go", Score: 0.7}},
	})

	got, err := SemanticDiff("old content", "new content", 3, "nomic")
	d.waitHandled(t)

	if err != nil || got == nil {
		t.Fatalf("unexpected: err=%v got=%v", err, got)
	}
	if got.Distance != 0.34 {
		t.Errorf("Distance = %v, want 0.34", got.Distance)
	}
	req := d.req()
	assertField(t, req, "type", "semantic_diff")
	assertField(t, req, "content_a", "old content")
	assertField(t, req, "content_b", "new content")
	assertField(t, req, "top_k", 3)
	assertField(t, req, "model", "nomic")
}

func TestWireProtocol_SemanticDiff_Omitempty(t *testing.T) {
	d := newTestDaemon(t, semanticDiffResp{})

	_, _ = SemanticDiff("a", "b", 0, "")
	d.waitHandled(t)

	req := d.req()
	assertNoField(t, req, "top_k")
	assertNoField(t, req, "model")
}

func TestWireProtocol_ExtractTerminology(t *testing.T) {
	d := newTestDaemon(t, terminologyResp{Terms: []TermItem{{DisplayName: "rate limiter", Score: 0.9}}})

	got, err := ExtractTerminology([]string{"file://a.go"}, 10)
	d.waitHandled(t)

	if err != nil || len(got) != 1 {
		t.Fatalf("unexpected: err=%v len=%d", err, len(got))
	}
	req := d.req()
	assertField(t, req, "type", "extract_terminology")
	assertField(t, req, "top_k", 10)
}

func TestWireProtocol_Outliers(t *testing.T) {
	d := newTestDaemon(t, outliersResp{Outliers: []NearestResult{{URI: "file://odd.go", Score: 0.1}}})

	got, err := Outliers([]string{"file://a.go", "file://b.go", "file://odd.go"}, 1)
	d.waitHandled(t)

	if err != nil || len(got) != 1 {
		t.Fatalf("unexpected: err=%v len=%d", err, len(got))
	}
	req := d.req()
	assertField(t, req, "type", "query_outliers")
	assertField(t, req, "top_k", 1)
}

// =============================================================================
// Response-size guard
// =============================================================================

func TestResponseSizeGuard(t *testing.T) {
	// Build a daemon that sends a response that exceeds the IndexStatus maxRespBytes (4<<10 = 4096).
	dir, err := os.MkdirTemp("/tmp", "lip")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	prev := os.Getenv("LIP_SOCKET")
	os.Setenv("LIP_SOCKET", sockPath)
	defer os.Setenv("LIP_SOCKET", prev)

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

		// Drain the request.
		var lenBuf [4]byte
		_, _ = io.ReadFull(conn, lenBuf[:])
		reqLen := binary.BigEndian.Uint32(lenBuf[:])
		_, _ = io.ReadFull(conn, make([]byte, reqLen))

		// Send a response that claims to be 8192 bytes — over the 4096 limit.
		oversized := uint32(8192)
		var respLen [4]byte
		binary.BigEndian.PutUint32(respLen[:], oversized)
		_, _ = conn.Write(respLen[:])
		// Don't send the body — client should bail after reading the length.
	}()

	got, err := IndexStatus()
	<-done

	if got != nil || err != nil {
		t.Errorf("oversized response: want (nil,nil), got (%v,%v)", got, err)
	}
}

// =============================================================================
// SocketPath
// =============================================================================

func TestSocketPath_EnvOverride(t *testing.T) {
	os.Setenv("LIP_SOCKET", "/tmp/custom.sock")
	defer os.Unsetenv("LIP_SOCKET")
	if got := SocketPath(); got != "/tmp/custom.sock" {
		t.Errorf("SocketPath = %q, want /tmp/custom.sock", got)
	}
}

func TestSocketPath_Default(t *testing.T) {
	os.Unsetenv("LIP_SOCKET")
	got := SocketPath()
	if got == "" {
		t.Error("SocketPath returned empty string")
	}
	if filepath.Base(got) != "lip.sock" {
		t.Errorf("SocketPath base = %q, want lip.sock", filepath.Base(got))
	}
}

func TestEncodeFrame(t *testing.T) {
	frame, err := encodeFrame([]byte(`{"type":"ping"}`))
	if err != nil {
		t.Fatalf("encodeFrame: %v", err)
	}
	if got := binary.BigEndian.Uint32(frame[:4]); got != 15 {
		t.Errorf("length prefix = %d, want 15", got)
	}
	if string(frame[4:]) != `{"type":"ping"}` {
		t.Errorf("payload = %q", frame[4:])
	}

	if _, err := encodeFrame(make([]byte, maxFrameBytes)); err != nil {
		t.Errorf("frame at the limit rejected: %v", err)
	}
	if _, err := encodeFrame(make([]byte, maxFrameBytes+1)); err == nil {
		t.Error("frame over the limit accepted")
	}
}
