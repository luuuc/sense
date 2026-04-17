package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/index/indextest"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestAdapterConformance(t *testing.T) {
	indextest.RunConformance(t, func(t *testing.T) index.Index {
		t.Helper()
		path := filepath.Join(t.TempDir(), "index.db")
		a, err := sqlite.Open(context.Background(), path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return a
	})
}

func TestOpenFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("MissingParentDirectory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "does-not-exist", "index.db")
		if _, err := sqlite.Open(ctx, path); err == nil {
			t.Fatal("Open with missing parent directory should error, got nil")
		}
	})

	t.Run("PathIsExistingDirectory", func(t *testing.T) {
		// The tempdir itself is a directory; opening it as a DB file must fail.
		if _, err := sqlite.Open(ctx, t.TempDir()); err == nil {
			t.Fatal("Open with path pointing at a directory should error, got nil")
		}
	})
}

func TestReopenPreservesData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	first, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	f := &model.File{
		Path:      "app/models/persist.rb",
		Language:  "ruby",
		Hash:      "persistence",
		IndexedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	fileID, err := first.WriteFile(ctx, f)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	symID, err := first.WriteSymbol(ctx, &model.Symbol{
		FileID:    fileID,
		Name:      "PersistMe",
		Qualified: "App::PersistMe",
		Kind:      model.KindClass,
		LineStart: 1,
		LineEnd:   10,
	})
	if err != nil {
		t.Fatalf("WriteSymbol: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	second, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
	})

	got, err := second.ReadSymbol(ctx, symID)
	if err != nil {
		t.Fatalf("ReadSymbol after reopen: %v", err)
	}
	if got.Symbol.Qualified != "App::PersistMe" {
		t.Errorf("Qualified = %q, want App::PersistMe", got.Symbol.Qualified)
	}
	if got.File.Path != f.Path {
		t.Errorf("File.Path = %q, want %q", got.File.Path, f.Path)
	}
	if !got.File.IndexedAt.Equal(f.IndexedAt) {
		t.Errorf("File.IndexedAt = %v, want %v", got.File.IndexedAt, f.IndexedAt)
	}
}
