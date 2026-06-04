package blast

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestInternalHelpers(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	t.Run("loadChildIDs-empty", func(t *testing.T) {
		res, err := loadChildIDs(ctx, db, nil)
		if err != nil || res != nil {
			t.Errorf("expected (nil, nil), got (%v, %v)", res, err)
		}
		res, err = loadChildIDs(ctx, db, []int64{})
		if err != nil || res != nil {
			t.Errorf("expected (nil, nil), got (%v, %v)", res, err)
		}
	})

	t.Run("loadTestsTargeting-empty", func(t *testing.T) {
		res, err := loadTestsTargeting(ctx, db, nil)
		if err != nil || res == nil || len(res) != 0 {
			t.Errorf("expected ([]string{}, nil), got (%v, %v)", res, err)
		}
		res, err = loadTestsTargeting(ctx, db, []int64{})
		if err != nil || res == nil || len(res) != 0 {
			t.Errorf("expected ([]string{}, nil), got (%v, %v)", res, err)
		}
	})
}

func TestSiblingSymbolIDs(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db := adapter.DB()
	t.Cleanup(func() { _ = db.Close() })

	var widgetID1, widgetID2, otherID int64
	err = adapter.InTx(ctx, func() error {
		f1, err := adapter.WriteFile(ctx, &model.File{
			Path: "widget.rb", Language: "ruby", Hash: "h1",
			Symbols: 1, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		f2, err := adapter.WriteFile(ctx, &model.File{
			Path: "widget_ext.rb", Language: "ruby", Hash: "h2",
			Symbols: 1, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		widgetID1, err = adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: f1, Name: "Widget", Qualified: "Widget",
			Kind: model.KindClass, LineStart: 1, LineEnd: 10,
		})
		if err != nil {
			return err
		}
		widgetID2, err = adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: f2, Name: "Widget", Qualified: "Widget",
			Kind: model.KindClass, LineStart: 20, LineEnd: 30,
		})
		if err != nil {
			return err
		}
		otherID, err = adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: f1, Name: "Other", Qualified: "Other",
			Kind: model.KindClass, LineStart: 40, LineEnd: 50,
		})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("finds-siblings", func(t *testing.T) {
		ids, err := SiblingSymbolIDs(ctx, db, widgetID1)
		if err != nil {
			t.Fatalf("SiblingSymbolIDs: %v", err)
		}
		if len(ids) != 2 {
			t.Fatalf("expected 2 sibling IDs (reopened class), got %d: %v", len(ids), ids)
		}
		if ids[0] != widgetID1 {
			t.Errorf("expected first sibling to be self (widgetID1=%d), got %d", widgetID1, ids[0])
		}
		found := false
		for _, id := range ids {
			if id == widgetID2 {
				found = true
			}
		}
		if !found {
			t.Errorf("expected widgetID2=%d in siblings, got %v", widgetID2, ids)
		}
	})

	t.Run("no-siblings", func(t *testing.T) {
		ids, err := SiblingSymbolIDs(ctx, db, otherID)
		if err != nil {
			t.Fatalf("SiblingSymbolIDs: %v", err)
		}
		if len(ids) != 1 || ids[0] != otherID {
			t.Errorf("expected only self [otherID=%d], got %v", otherID, ids)
		}
	})
}

func TestLoadSymbolsBulk(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db := adapter.DB()
	t.Cleanup(func() { _ = db.Close() })

	var ids []int64
	err = adapter.InTx(ctx, func() error {
		f, err := adapter.WriteFile(ctx, &model.File{
			Path: "svc.go", Language: "go", Hash: "h1",
			Symbols: 3, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		for i, name := range []string{"pkg.FnA", "pkg.FnB", "pkg.FnC"} {
			id, err := adapter.WriteSymbol(ctx, &model.Symbol{
				FileID: f, Name: name, Qualified: name,
				Kind: model.KindFunction, LineStart: 1 + i, LineEnd: 10 + i,
			})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	m, err := loadSymbols(ctx, db, ids)
	if err != nil {
		t.Fatalf("loadSymbols: %v", err)
	}
	if len(m) != 3 {
		t.Errorf("expected 3 symbols, got %d", len(m))
	}
	for _, id := range ids {
		if _, ok := m[id]; !ok {
			t.Errorf("expected symbol with id %d in map", id)
		}
	}
}

func TestLoadSymbolsEmpty(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := loadSymbols(ctx, db, nil)
	if err != nil {
		t.Fatalf("loadSymbols(nil): %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}

	m, err = loadSymbols(ctx, db, []int64{})
	if err != nil {
		t.Fatalf("loadSymbols(empty): %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

func TestClassifyTierMemberKind(t *testing.T) {
	// The "member" edge kind is assigned internally by the BFS when a
	// type's own methods seed the frontier. Even though children are
	// excluded from blast output, classifyTier must still return the
	// correct tier for completeness.
	if got := classifyTier("member"); got != TierBreaks {
		t.Errorf("classifyTier(%q) = %d, want %d (TierBreaks)", "member", got, TierBreaks)
	}
	if got := classifyTier("calls"); got != TierBreaks {
		t.Errorf("classifyTier(%q) = %d, want %d (TierBreaks)", "calls", got, TierBreaks)
	}
	if got := classifyTier("composes"); got != TierReferences {
		t.Errorf("classifyTier(%q) = %d, want %d (TierReferences)", "composes", got, TierReferences)
	}
}

func TestSiblingSymbolIDsNonExistent(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db := adapter.DB()
	t.Cleanup(func() { _ = db.Close() })

	ids, err := SiblingSymbolIDs(ctx, db, 999999)
	if err != nil {
		t.Fatalf("SiblingSymbolIDs with non-existent ID: %v", err)
	}
	if len(ids) != 1 || ids[0] != 999999 {
		t.Errorf("expected [999999] (self always included), got %v", ids)
	}
}

// hasTemporal reports temporal coupling from either a direct temporal caller
// or an indirect (multi-hop) one; the indirect path is the branch the full
// Compute tests rarely exercise on its own.
func TestHasTemporal(t *testing.T) {
	if !hasTemporal(map[int64]bool{1: true}, nil) {
		t.Error("direct temporal caller should report temporal coupling")
	}
	if !hasTemporal(nil, []CallerHop{{ViaTemporal: true}}) {
		t.Error("indirect temporal hop should report temporal coupling")
	}
	if hasTemporal(nil, []CallerHop{{ViaTemporal: false}}) {
		t.Error("no temporal edges should report none")
	}
}
