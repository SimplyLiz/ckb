package storage

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMetricsStore(t *testing.T) {
	// Create temp directory for test database
	tmpDir, err := os.MkdirTemp("", "ckb-metrics-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Open database (will create with v10 schema)
	db, err := Open(tmpDir, logger)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify the table exists
	var tableName string
	err = db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='wide_result_metrics'
	`).Scan(&tableName)
	if err != nil {
		t.Fatalf("wide_result_metrics table not found: %v", err)
	}

	// Test RecordWideResult
	err = db.RecordWideResult("findReferences", 100, 50, 50, 1000, 4000, 25)
	if err != nil {
		t.Fatalf("failed to record wide result: %v", err)
	}

	err = db.RecordWideResult("findReferences", 200, 100, 100, 2000, 8000, 50)
	if err != nil {
		t.Fatalf("failed to record second wide result: %v", err)
	}

	err = db.RecordWideResult("getCallGraph", 10, 10, 0, 500, 2000, 10)
	if err != nil {
		t.Fatalf("failed to record getCallGraph result: %v", err)
	}

	// Test GetWideResultAggregates
	since := time.Now().Add(-24 * time.Hour)
	aggregates, err := db.GetWideResultAggregates(since)
	if err != nil {
		t.Fatalf("failed to get aggregates: %v", err)
	}

	// Verify findReferences aggregate
	fr, ok := aggregates["findReferences"]
	if !ok {
		t.Fatal("findReferences not found in aggregates")
	}
	if fr.QueryCount != 2 {
		t.Errorf("expected QueryCount=2, got %d", fr.QueryCount)
	}
	if fr.TotalResults != 300 {
		t.Errorf("expected TotalResults=300, got %d", fr.TotalResults)
	}
	if fr.TotalReturned != 150 {
		t.Errorf("expected TotalReturned=150, got %d", fr.TotalReturned)
	}
	if fr.TotalTruncated != 150 {
		t.Errorf("expected TotalTruncated=150, got %d", fr.TotalTruncated)
	}
	if fr.TotalTokens != 3000 {
		t.Errorf("expected TotalTokens=3000, got %d", fr.TotalTokens)
	}
	if fr.TotalMs != 75 {
		t.Errorf("expected TotalMs=75, got %d", fr.TotalMs)
	}
	if fr.TotalBytes != 12000 { // 4000 + 8000
		t.Errorf("expected TotalBytes=12000, got %d", fr.TotalBytes)
	}

	// Verify getCallGraph aggregate
	cg, ok := aggregates["getCallGraph"]
	if !ok {
		t.Fatal("getCallGraph not found in aggregates")
	}
	if cg.QueryCount != 1 {
		t.Errorf("expected QueryCount=1, got %d", cg.QueryCount)
	}
	if cg.TotalTruncated != 0 {
		t.Errorf("expected TotalTruncated=0, got %d", cg.TotalTruncated)
	}
}

func TestGetWideResultRecords(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ckb-metrics-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := Open(tmpDir, logger)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Insert some records
	for i := 0; i < 10; i++ {
		err = db.RecordWideResult("testTool", 100, 50, 50, 1000, 4000, int64(i))
		if err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}

	// Test limit
	records, err := db.GetWideResultRecords(5, "")
	if err != nil {
		t.Fatalf("failed to get records: %v", err)
	}
	if len(records) != 5 {
		t.Errorf("expected 5 records, got %d", len(records))
	}

	// Test filter
	records, err = db.GetWideResultRecords(100, "nonexistent")
	if err != nil {
		t.Fatalf("failed to get filtered records: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records for nonexistent tool, got %d", len(records))
	}

	records, err = db.GetWideResultRecords(100, "testTool")
	if err != nil {
		t.Fatalf("failed to get filtered records: %v", err)
	}
	if len(records) != 10 {
		t.Errorf("expected 10 records for testTool, got %d", len(records))
	}
}

func TestCleanupOldMetrics(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ckb-metrics-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := Open(tmpDir, logger)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Insert a record
	err = db.RecordWideResult("testTool", 100, 50, 50, 1000, 4000, 10)
	if err != nil {
		t.Fatalf("failed to record: %v", err)
	}

	// Verify it exists
	records, err := db.GetWideResultRecords(10, "")
	if err != nil {
		t.Fatalf("failed to get records: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record, got %d", len(records))
	}

	// Cleanup with 1 hour retention (should not delete the record we just added)
	deleted, err := db.CleanupOldMetrics(time.Hour)
	if err != nil {
		t.Fatalf("failed to cleanup: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}

	// Verify record still exists
	records, err = db.GetWideResultRecords(10, "")
	if err != nil {
		t.Fatalf("failed to get records: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record after cleanup, got %d", len(records))
	}
}

func TestGetWideResultStats(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ckb-metrics-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := Open(tmpDir, logger)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Stats on empty table
	total, _, _, err := db.GetWideResultStats()
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if total != 0 {
		t.Errorf("expected 0 total on empty table, got %d", total)
	}

	// Add some records
	err = db.RecordWideResult("tool1", 10, 5, 5, 100, 400, 10)
	if err != nil {
		t.Fatalf("failed to record: %v", err)
	}
	time.Sleep(10 * time.Millisecond) // ensure different timestamps
	err = db.RecordWideResult("tool2", 20, 10, 10, 200, 800, 20)
	if err != nil {
		t.Fatalf("failed to record: %v", err)
	}

	total, oldest, newest, err := db.GetWideResultStats()
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 total, got %d", total)
	}
	if oldest == nil || newest == nil {
		t.Error("expected non-nil oldest and newest")
	}
}

func TestWideResultAggregateCalculations(t *testing.T) {
	agg := &WideResultAggregate{
		ToolName:       "test",
		QueryCount:     10,
		TotalResults:   1000,
		TotalReturned:  500,
		TotalTruncated: 500,
		TotalTokens:    10000,
		TotalMs:        1000,
	}

	// Test AvgTruncationRate
	rate := agg.AvgTruncationRate()
	if rate != 0.5 {
		t.Errorf("expected AvgTruncationRate=0.5, got %f", rate)
	}

	// Test AvgTokens
	avgTokens := agg.AvgTokens()
	if avgTokens != 1000 {
		t.Errorf("expected AvgTokens=1000, got %f", avgTokens)
	}

	// Test AvgLatencyMs
	avgLatency := agg.AvgLatencyMs()
	if avgLatency != 100 {
		t.Errorf("expected AvgLatencyMs=100, got %f", avgLatency)
	}

	// Test zero division protection
	emptyAgg := &WideResultAggregate{}
	if emptyAgg.AvgTruncationRate() != 0 {
		t.Error("expected 0 for empty TotalResults")
	}
	if emptyAgg.AvgTokens() != 0 {
		t.Error("expected 0 for empty QueryCount")
	}
	if emptyAgg.AvgLatencyMs() != 0 {
		t.Error("expected 0 for empty QueryCount")
	}
}

func TestSchemaV10Migration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ckb-migration-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create .ckb directory
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("failed to create .ckb dir: %v", err)
	}

	// Open database fresh - will use full v10 schema
	db, err := Open(tmpDir, logger)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	// Check schema version
	var version int
	err = db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err != nil {
		t.Fatalf("failed to get schema version: %v", err)
	}
	if version != currentSchemaVersion {
		t.Errorf("expected schema version %d, got %d", currentSchemaVersion, version)
	}

	_ = db.Close()
}
