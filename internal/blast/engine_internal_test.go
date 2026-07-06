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

// capResults must keep production callers over test-file callers when the cap
// bites: a test fixture constructing the subject rides a 1.0 call edge and
// would otherwise evict every 0.9 production dependent — the callers an
// impact audit actually needs (netbox Device: 49 setUpTestData callers vs the
// scattered ipam/virtualization dependents).
func TestCapResultsPrefersProductionOverTests(t *testing.T) {
	const prodID, testID = int64(1), int64(2)
	s := &bfsState{pathConf: map[int64]float64{prodID: 0.9, testID: 1.0}}
	flags := map[int64]bool{prodID: false, testID: true}

	direct, indirect := s.capResults([]int64{prodID, testID}, nil, flags, 1)
	if len(indirect) != 0 {
		t.Fatalf("expected no indirect callers, got %v", indirect)
	}
	if len(direct) != 1 || direct[0] != prodID {
		t.Errorf("cap must keep the production caller over the higher-confidence test caller: got %v, want [%d]", direct, prodID)
	}

	// Under the cap nothing is trimmed or reordered.
	direct, _ = s.capResults([]int64{prodID, testID}, nil, flags, 10)
	if len(direct) != 2 {
		t.Errorf("no-cap path must return all callers, got %v", direct)
	}
}

// Production-over-test is the PRIMARY sort key — above confidence AND above
// direct-over-indirect. An indirect production caller at 0.3 must evict a
// direct test caller at 1.0. If a future edit reorders the keys, this test is
// what notices.
func TestCapResultsProductionOutranksConfidenceAndDirectness(t *testing.T) {
	const testDirectID, prodIndirectID = int64(1), int64(2)
	s := &bfsState{pathConf: map[int64]float64{testDirectID: 1.0, prodIndirectID: 0.3}}
	flags := map[int64]bool{testDirectID: true, prodIndirectID: false}

	direct, indirect := s.capResults([]int64{testDirectID}, []int64{prodIndirectID}, flags, 1)
	if len(direct) != 0 {
		t.Errorf("direct test caller must be evicted, got %v", direct)
	}
	if len(indirect) != 1 || indirect[0] != prodIndirectID {
		t.Errorf("indirect production caller must survive the cap: got %v, want [%d]", indirect, prodIndirectID)
	}
}

// A nil flag map is the documented degradation contract (testFileFlags returns
// nil on query failure): every caller reads as production and the ranking
// falls back to the previous confidence-only order.
func TestCapResultsNilFlagsKeepsConfidenceOrder(t *testing.T) {
	const prodID, testID = int64(1), int64(2)
	s := &bfsState{pathConf: map[int64]float64{prodID: 0.9, testID: 1.0}}

	direct, _ := s.capResults([]int64{prodID, testID}, nil, nil, 1)
	if len(direct) != 1 || direct[0] != testID {
		t.Errorf("nil flags must degrade to confidence-only order (test caller at 1.0 kept): got %v, want [%d]", direct, testID)
	}
}

func TestTestFileFlags(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db := adapter.DB()
	t.Cleanup(func() { _ = db.Close() })

	var prodID, testID int64
	err = adapter.InTx(ctx, func() error {
		fProd, err := adapter.WriteFile(ctx, &model.File{
			Path: "app/filters.py", Language: "python", Hash: "h1",
			Symbols: 1, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		fTest, err := adapter.WriteFile(ctx, &model.File{
			Path: "app/tests/test_api.py", Language: "python", Hash: "h2",
			Symbols: 1, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		prodID, err = adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fProd, Name: "filter_device", Qualified: "F.filter_device",
			Kind: model.KindMethod, LineStart: 1, LineEnd: 5,
		})
		if err != nil {
			return err
		}
		testID, err = adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fTest, Name: "setUpTestData", Qualified: "T.setUpTestData",
			Kind: model.KindMethod, LineStart: 1, LineEnd: 5,
		})
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	flags := testFileFlags(ctx, db, []int64{prodID, testID})
	if flags == nil {
		t.Fatal("expected a flag map from a healthy index, got nil")
	}
	if flags[prodID] {
		t.Errorf("production symbol flagged as test")
	}
	if !flags[testID] {
		t.Errorf("test-file symbol not flagged")
	}
}

// A database without the sense schema exercises the best-effort contract:
// testFileFlags returns nil rather than failing, and the caller's nil-map
// reads degrade the ranking to confidence-only (covered above).
func TestTestFileFlagsNoSchemaReturnsNil(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if flags := testFileFlags(context.Background(), db, []int64{1, 2}); flags != nil {
		t.Errorf("expected nil flags on a schema-less database, got %v", flags)
	}
}

// Mirrors mcpio's TestIsTestPath table: the blast copy must agree with
// mcpio.IsTestPath (the presentation layer buckets the same paths), and the
// import cycle rules out asserting parity directly.
func TestIsTestPathLocalCopy(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"internal/foo/foo_test.go", true},
		{"src/app.test.ts", true},
		{"test/helpers.rb", true},
		{"tests/unit/auth.py", true},
		{"spec/models/user_spec.rb", true},
		{"internal/testdata/fixture.json", true},
		{"src/main/java/com/example/UserTest.java", true},
		{"src/test/kotlin/TestUser.kt", true},
		{"src/test/java/UserTests.java", true},
		{"lib/test_auth.py", true},
		{"src/main/java/com/example/User.java", false},
		{"src/main/java/com/example/TestUtils.java", false},
		{"src/main/java/com/example/Contest.java", false},
		{"internal/foo/foo.go", false},
		{"lib/auth.rb", false},
	}
	for _, tt := range tests {
		if got := isTestPath(tt.path); got != tt.want {
			t.Errorf("isTestPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
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
