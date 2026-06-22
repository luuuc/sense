package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// setupGitRepo creates a temp directory with a git repo and a valid
// .sense database seeded with symbols that map to committed files.
func setupGitRepo(t *testing.T) (dir string, h *handlers, cleanup func()) {
	t.Helper()
	dir = t.TempDir()

	// Init git repo
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	_ = exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()

	// Create a file and commit it
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("git", "-C", dir, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "initial").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// Create .sense database
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	f := model.File{Path: "main.go", Language: "go", Hash: "abc", Symbols: 1, IndexedAt: now}
	fileID, err := adapter.WriteFile(ctx, &f)
	if err != nil {
		t.Fatal(err)
	}

	s := &model.Symbol{
		FileID:    fileID,
		Name:      "main",
		Qualified: "main.main",
		Kind:      "function",
		LineStart: 1,
		LineEnd:   5,
		Snippet:   "func main() {}",
	}
	symID, err := adapter.WriteSymbol(ctx, s)
	if err != nil {
		t.Fatal(err)
	}

	// Create a second symbol that calls main.main so blast.Compute has callers
	f2 := model.File{Path: "caller.go", Language: "go", Hash: "def", Symbols: 1, IndexedAt: now}
	fileID2, err := adapter.WriteFile(ctx, &f2)
	if err != nil {
		t.Fatal(err)
	}

	s2 := &model.Symbol{
		FileID:    fileID2,
		Name:      "Run",
		Qualified: "main.Run",
		Kind:      "function",
		LineStart: 1,
		LineEnd:   5,
		Snippet:   "func Run() { main() }",
	}
	symID2, err := adapter.WriteSymbol(ctx, s2)
	if err != nil {
		t.Fatal(err)
	}

	// Edge: main.Run -> main.main (so main.main has a caller)
	edge := model.Edge{
		SourceID:   &symID2,
		TargetID:   symID,
		Kind:       model.EdgeCalls,
		FileID:     fileID2,
		Line:       intPtr(1),
		Confidence: 1.0,
	}
	if _, err := adapter.WriteEdge(ctx, &edge); err != nil {
		t.Fatal(err)
	}

	engine := &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		seenSymbols: make(map[int64]bool),
	}

	cleanup = func() {
		_ = adapter.Close()
	}

	return dir, engine, cleanup
}

func TestBlastDiffEmptyDiff(t *testing.T) {
	_, h, cleanup := setupGitRepo(t)
	defer cleanup()

	ctx := context.Background()
	resp, err := h.blastDiff(ctx, "HEAD", blast.Options{MaxHops: 1}, nil)
	if err != nil {
		t.Fatalf("blastDiff empty diff: %v", err)
	}

	// No changes between HEAD and working tree -> empty diff -> no affected symbols
	if resp.TotalAffected != 0 {
		t.Errorf("TotalAffected = %d, want 0 for empty diff", resp.TotalAffected)
	}
	if resp.ProductionAffected != 0 {
		t.Errorf("ProductionAffected = %d, want 0 for empty diff", resp.ProductionAffected)
	}
}

func TestBlastDiffSingleChange(t *testing.T) {
	dir, h, cleanup := setupGitRepo(t)
	defer cleanup()

	// Modify the file but don't commit — diff against HEAD will show the change
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() { println(\"hello\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	resp, err := h.blastDiff(ctx, "HEAD", blast.Options{MaxHops: 1}, nil)
	if err != nil {
		t.Fatalf("blastDiff single change: %v", err)
	}
	t.Logf("resp: TotalAffected=%d ProductionAffected=%d Symbol=%q", resp.TotalAffected, resp.ProductionAffected, resp.Symbol)

	// HEAD shows diff between HEAD and working tree
	// main.go changed, so main.main should be in blast results
	if resp.TotalAffected == 0 {
		t.Error("Expected TotalAffected > 0 for single file change")
	}
	if resp.ProductionAffected == 0 {
		t.Error("Expected ProductionAffected > 0 for changed file")
	}
}

func TestBlastDiffInvalidRef(t *testing.T) {
	_, h, cleanup := setupGitRepo(t)
	defer cleanup()

	ctx := context.Background()
	_, err := h.blastDiff(ctx, "invalid-ref-12345", blast.Options{MaxHops: 1}, nil)
	if err == nil {
		t.Fatal("expected error for invalid git ref")
	}
	// Should be a git error, not a panic
	t.Logf("Got expected error: %v", err)
}

func TestBlastDiffNonGitDirectory(t *testing.T) {
	// Use regular test server (no git repo)
	ts := setupTestServer(t)
	ctx := context.Background()

	_, err := ts.handlers.blastDiff(ctx, "HEAD", blast.Options{MaxHops: 1}, nil)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	// Should error gracefully, not panic
	t.Logf("Got expected error: %v", err)
}

func TestBlastDiffMultipleFiles(t *testing.T) {
	dir, h, cleanup := setupGitRepo(t)
	defer cleanup()

	// Modify main.go and add util.go
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() { println(\"hello\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	utilFile := filepath.Join(dir, "util.go")
	if err := os.WriteFile(utilFile, []byte("package main\nfunc helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Index the new file in the database
	ctx := context.Background()
	now := time.Now()
	f := model.File{Path: "util.go", Language: "go", Hash: "def", Symbols: 1, IndexedAt: now}
	fileID, err := h.adapter.WriteFile(ctx, &f)
	if err != nil {
		t.Fatal(err)
	}

	s := &model.Symbol{
		FileID:    fileID,
		Name:      "helper",
		Qualified: "main.helper",
		Kind:      "function",
		LineStart: 1,
		LineEnd:   5,
		Snippet:   "func helper() {}",
	}
	symID, err := h.adapter.WriteSymbol(ctx, s)
	if err != nil {
		t.Fatal(err)
	}

	edge := model.Edge{
		SourceID:   &symID,
		TargetID:   symID,
		Kind:       model.EdgeCalls,
		FileID:     fileID,
		Line:       intPtr(1),
		Confidence: 1.0,
	}
	if _, err := h.adapter.WriteEdge(ctx, &edge); err != nil {
		t.Fatal(err)
	}

	// Add to index but don't commit — both files show in diff against HEAD
	if out, err := exec.Command("git", "-C", dir, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	resp, err := h.blastDiff(ctx, "HEAD", blast.Options{MaxHops: 1}, nil)
	if err != nil {
		t.Fatalf("blastDiff multiple files: %v", err)
	}

	if resp.TotalAffected < 1 {
		t.Errorf("TotalAffected = %d, want >= 1 for multiple changed files", resp.TotalAffected)
	}
}
