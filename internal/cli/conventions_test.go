package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func seedConventionsProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	now := time.Now()
	files := []model.File{
		{Path: "app/services/a_service.rb", Language: "ruby", Hash: "a1", Symbols: 1, IndexedAt: now},
		{Path: "app/services/b_service.rb", Language: "ruby", Hash: "a2", Symbols: 1, IndexedAt: now},
		{Path: "app/services/c_service.rb", Language: "ruby", Hash: "a3", Symbols: 1, IndexedAt: now},
		{Path: "app/services/base.rb", Language: "ruby", Hash: "a4", Symbols: 1, IndexedAt: now},
	}
	fileIDs := make(map[string]int64)
	for i := range files {
		id, err := adapter.WriteFile(ctx, &files[i])
		if err != nil {
			t.Fatal(err)
		}
		fileIDs[files[i].Path] = id
	}

	baseID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs["app/services/base.rb"], Name: "BaseService", Qualified: "BaseService",
		Kind: "class", LineStart: 1, LineEnd: 5,
	})
	symIDs := make([]int64, 3)
	for i, name := range []string{"AService", "BService", "CService"} {
		id, _ := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fileIDs[files[i].Path], Name: name, Qualified: name,
			Kind: "class", LineStart: 1, LineEnd: 10,
		})
		symIDs[i] = id
	}
	for i := 0; i < 3; i++ {
		if _, err := adapter.WriteEdge(ctx, &model.Edge{
			SourceID: symIDs[i], TargetID: baseID,
			Kind: "inherits", FileID: fileIDs[files[i].Path], Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func TestRunConventionsHuman(t *testing.T) {
	dir := seedConventionsProject(t)
	var stdout, stderr bytes.Buffer
	code := RunConventions(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "BaseService") {
		t.Errorf("expected BaseService in output, got:\n%s", out)
	}
}

func TestRunConventionsJSON(t *testing.T) {
	dir := seedConventionsProject(t)
	var stdout, stderr bytes.Buffer
	code := RunConventions([]string{"--json"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var resp mcpio.ConventionsResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if resp.SenseMetrics.SymbolsAnalyzed == 0 {
		t.Error("expected non-zero symbols_analyzed")
	}
	if resp.SenseMetrics.EstimatedFileReadsAvoided != nil {
		t.Error("estimated_file_reads_avoided should be null")
	}
}

func TestRunConventionsMinStrength(t *testing.T) {
	dir := seedConventionsProject(t)
	var stdout, stderr bytes.Buffer
	code := RunConventions([]string{"--json", "--min-strength", "0.99"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var resp mcpio.ConventionsResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, c := range resp.Conventions {
		if float64(c.Strength) < 0.99 {
			t.Errorf("convention below threshold: %v", c)
		}
	}
}

func TestRunConventionsMissingIndex(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := RunConventions(nil, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexMissing {
		t.Errorf("expected exit %d, got %d", ExitIndexMissing, code)
	}
}
