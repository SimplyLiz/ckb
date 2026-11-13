package deadcode

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestAnalyzeWithoutIndexIsNotAnalyzed(t *testing.T) {
	a := NewAnalyzer(nil, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	res, err := a.Analyze(context.Background(), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if res.Analyzed || res.NotAnalyzedReason == "" || len(res.DeadCode) != 0 {
		t.Errorf("got analyzed=%v reason=%q items=%d", res.Analyzed, res.NotAnalyzedReason, len(res.DeadCode))
	}
}
