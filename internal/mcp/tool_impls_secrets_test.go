package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/secrets"
)

// scanSecrets used to answer with filesScanned 0 for every scope and
// scannedAt 0001-01-01 for staged and history; it now goes through
// Scanner.Scan for all of them.
func TestScanSecretsReportsScanMetadata(t *testing.T) {
	s := newTestMCPServer(t)
	root := s.engine().GetRepoRoot()
	if err := os.WriteFile(filepath.Join(root, "run.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := s.toolScanSecrets(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	res, ok := resp.Data.(*secrets.ScanResult)
	if !ok {
		t.Fatalf("data is %T", resp.Data)
	}
	if res.ScannedAt.IsZero() {
		t.Error("scannedAt not set")
	}
	if res.Summary.FilesScanned == 0 {
		t.Error("filesScanned 0")
	}
}
