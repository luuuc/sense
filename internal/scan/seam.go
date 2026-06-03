package scan

import (
	"context"
	"database/sql"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// indexStore is the seam the scan harness writes and reads the index
// through. It is the exact method set the harness already calls on
// *sqlite.Adapter — nothing wider — promoted from a concrete dependency to
// an interface so the real-world effect (the database) sits behind a seam a
// caller controls, per cycle 27's no-side-effects goal. Production wires the
// concrete *sqlite.Adapter, which satisfies this unchanged; a white-box test
// can substitute an adapter that fails one named method to drive the
// harness's rollback and error-return branches without a SQL-driver shim.
//
// It is deliberately unexported and scan-local: this is a test seam, not a
// new public storage contract (that boundary is internal/index). Methods are
// grouped by role to keep the surface legible, not to suggest sub-seams.
type indexStore interface {
	// Transactions and prepared statements — the batched write path.
	InTx(ctx context.Context, fn func() error) error
	PrepareSymbolStmt(ctx context.Context) (*sql.Stmt, error)
	PrepareEdgeStmt(ctx context.Context) (*sql.Stmt, error)
	PrepareEmbeddingStmt(ctx context.Context) (*sql.Stmt, error)

	// Row writes.
	WriteFile(ctx context.Context, f *model.File) (int64, error)
	WriteSymbol(ctx context.Context, s *model.Symbol) (int64, error)
	WriteEdge(ctx context.Context, e *model.Edge) (int64, error)
	DeleteFile(ctx context.Context, path string) error

	// Reads used across the resolve, temporal, satisfy and embed passes.
	Query(ctx context.Context, f index.Filter) ([]model.Symbol, error)
	SymbolRefs(ctx context.Context) ([]model.SymbolRef, error)
	FileMeta(ctx context.Context, path string) (int64, string, error)
	FileHashMap(ctx context.Context) (map[string]sqlite.CachedFile, error)
	FilePaths(ctx context.Context) ([]string, error)
	FilePathsByIDs(ctx context.Context, fileIDs []int64) (map[int64]string, error)
	FileIDsByLanguage(ctx context.Context, lang string) (map[int64]bool, error)
	EdgesOfKind(ctx context.Context, kind model.EdgeKind) ([]model.Edge, error)
	ContextForFile(ctx context.Context, fileID int64) (map[int64]string, error)
	SymbolsForFiles(ctx context.Context, fileIDs []int64) ([]sqlite.EmbedSymbol, error)

	// Metadata and embeddings.
	ReadMeta(ctx context.Context, key string) (string, error)
	WriteMeta(ctx context.Context, key, value string) error
	DeleteMeta(ctx context.Context, key string) error
	ClearEmbeddings(ctx context.Context) error

	// DB exposes the handle the temporal and satisfy passes run raw SQL on.
	DB() *sql.DB
}

// Compile-time proof the production adapter satisfies the seam, so a drift
// in either surface fails the build rather than a test.
var _ indexStore = (*sqlite.Adapter)(nil)
