package sqlite_test

import (
	"context"
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// openTestDB creates a fresh in-tempdir database for a test.
func openTestDB(t *testing.T) *sqlite.Adapter {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	a, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// seedFile inserts a file and returns its ID.
func seedFile(t *testing.T, a *sqlite.Adapter, path, lang, hash string) int64 {
	t.Helper()
	ctx := context.Background()
	fid, err := a.WriteFile(ctx, &model.File{
		Path: path, Language: lang, Hash: hash,
		Symbols: 0, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return fid
}

// seedSymbol inserts a symbol and returns its ID.
func seedSymbol(t *testing.T, a *sqlite.Adapter, fid int64, name, qualified, kind string) int64 {
	t.Helper()
	ctx := context.Background()
	sid, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: name, Qualified: qualified,
		Kind: model.SymbolKind(kind), LineStart: 1, LineEnd: 10,
	})
	if err != nil {
		t.Fatalf("WriteSymbol(%s): %v", qualified, err)
	}
	return sid
}

// seedSymbolWithParent inserts a symbol with a parent_id and returns its ID.
func seedSymbolWithParent(t *testing.T, a *sqlite.Adapter, fid int64, name, qualified, kind string, parentID int64) int64 {
	t.Helper()
	ctx := context.Background()
	sid, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: name, Qualified: qualified,
		Kind: model.SymbolKind(kind), LineStart: 1, LineEnd: 10,
		ParentID: &parentID,
	})
	if err != nil {
		t.Fatalf("WriteSymbol(%s): %v", qualified, err)
	}
	return sid
}

// floatVec encodes float32 values into a little-endian byte slice.
func floatVec(values ...float32) []byte {
	buf := make([]byte, len(values)*4)
	for i, v := range values {
		bits := math.Float32bits(v)
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf
}
