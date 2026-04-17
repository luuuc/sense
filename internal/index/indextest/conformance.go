// Package indextest contains the conformance suite every Index adapter
// must pass. It lives in a sibling package so the production index package
// stays free of any testing-library dependency — the same pattern stdlib
// uses for net/http/httptest and testing/fstest.
package indextest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
)

// Factory opens a fresh, empty Index for a test. Each call must return an
// index with no pre-existing rows; implementations typically back it with
// t.TempDir().
type Factory func(t *testing.T) index.Index

// RunConformance exercises every guarantee of the Index contract against
// the implementation returned by newIdx. Adapters should call this from
// their test file to prove they satisfy the contract.
func RunConformance(t *testing.T, newIdx Factory) {
	t.Helper()
	cases := []struct {
		name string
		run  func(*testing.T, Factory)
	}{
		{"FileRoundTrip", testFileRoundTrip},
		{"FileUpsertIsIdempotent", testFileUpsertIdempotent},
		{"SymbolRoundTrip", testSymbolRoundTrip},
		{"SymbolUpsertIsIdempotent", testSymbolUpsertIdempotent},
		{"SymbolSameQualifiedDifferentFileIsDistinct", testSymbolSameQualifiedDifferentFile},
		{"SymbolNullablesRoundTrip", testSymbolNullablesRoundTrip},
		{"EdgeRoundTrip", testEdgeRoundTrip},
		{"EdgeUpsertIsIdempotent", testEdgeUpsertIdempotent},
		{"ReadSymbolNotFound", testReadSymbolNotFound},
		{"QueryEmpty", testQueryEmpty},
		{"QueryByKind", testQueryByKind},
		{"QueryByFileID", testQueryByFileID},
		{"QueryByName", testQueryByName},
		{"QueryLimit", testQueryLimit},
		{"Clear", testClear},
		{"Close", testClose},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newIdx) })
	}
}

// ---------- helpers ----------

// open builds a fresh index and registers Close as a cleanup hook.
func open(t *testing.T, newIdx Factory) index.Index {
	t.Helper()
	idx := newIdx(t)
	t.Cleanup(func() {
		if err := idx.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return idx
}

func mustWriteFile(t *testing.T, ctx context.Context, idx index.Index, f *model.File) int64 {
	t.Helper()
	id, err := idx.WriteFile(ctx, f)
	if err != nil {
		t.Fatalf("WriteFile(%q): %v", f.Path, err)
	}
	return id
}

func mustWriteSymbol(t *testing.T, ctx context.Context, idx index.Index, s *model.Symbol) int64 {
	t.Helper()
	id, err := idx.WriteSymbol(ctx, s)
	if err != nil {
		t.Fatalf("WriteSymbol(%q): %v", s.Qualified, err)
	}
	return id
}

func mustWriteEdge(t *testing.T, ctx context.Context, idx index.Index, e *model.Edge) int64 {
	t.Helper()
	id, err := idx.WriteEdge(ctx, e)
	if err != nil {
		t.Fatalf("WriteEdge(%d→%d %q): %v", e.SourceID, e.TargetID, e.Kind, err)
	}
	return id
}

func sampleFile(path string) *model.File {
	return &model.File{
		Path:      path,
		Language:  "ruby",
		Hash:      "deadbeef",
		Symbols:   0,
		IndexedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
}

func sampleSymbol(fileID int64, name, qualified string, kind model.SymbolKind) *model.Symbol {
	return &model.Symbol{
		FileID:     fileID,
		Name:       name,
		Qualified:  qualified,
		Kind:       kind,
		Visibility: "public",
		LineStart:  1,
		LineEnd:    10,
		Snippet:    "def " + name + "; end",
	}
}

func sampleEdge(sourceID, targetID, fileID int64, kind model.EdgeKind) *model.Edge {
	line := 3
	return &model.Edge{
		SourceID:   sourceID,
		TargetID:   targetID,
		FileID:     fileID,
		Kind:       kind,
		Line:       &line,
		Confidence: 1.0,
	}
}

// ---------- tests ----------

func testFileRoundTrip(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	f := sampleFile("app/services/checkout_service.rb")
	fileID := mustWriteFile(t, ctx, idx, f)
	if fileID == 0 {
		t.Fatal("WriteFile returned zero id")
	}

	s := sampleSymbol(fileID, "CheckoutService", "App::Services::CheckoutService", model.KindClass)
	symID := mustWriteSymbol(t, ctx, idx, s)

	got, err := idx.ReadSymbol(ctx, symID)
	if err != nil {
		t.Fatalf("ReadSymbol: %v", err)
	}
	if got.File.ID != fileID {
		t.Errorf("File.ID = %d, want %d", got.File.ID, fileID)
	}
	if got.File.Path != f.Path {
		t.Errorf("File.Path = %q, want %q", got.File.Path, f.Path)
	}
	if got.File.Language != f.Language {
		t.Errorf("File.Language = %q, want %q", got.File.Language, f.Language)
	}
	if !got.File.IndexedAt.Equal(f.IndexedAt) {
		t.Errorf("File.IndexedAt = %v, want %v", got.File.IndexedAt, f.IndexedAt)
	}
}

func testFileUpsertIdempotent(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	f := sampleFile("app/models/user.rb")
	id1 := mustWriteFile(t, ctx, idx, f)

	f2 := sampleFile("app/models/user.rb")
	f2.Hash = "cafef00d" // non-key field change
	id2 := mustWriteFile(t, ctx, idx, f2)

	if id1 != id2 {
		t.Errorf("upsert returned new id: first=%d second=%d", id1, id2)
	}
}

func testSymbolRoundTrip(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileID := mustWriteFile(t, ctx, idx, sampleFile("app/models/user.rb"))
	s := sampleSymbol(fileID, "email_verified?", "User#email_verified?", model.KindMethod)
	s.LineStart = 12
	s.LineEnd = 18
	s.Docstring = "Whether the user's email has been confirmed."

	symID := mustWriteSymbol(t, ctx, idx, s)

	got, err := idx.ReadSymbol(ctx, symID)
	if err != nil {
		t.Fatalf("ReadSymbol: %v", err)
	}
	if got.Symbol.Name != s.Name {
		t.Errorf("Name = %q, want %q", got.Symbol.Name, s.Name)
	}
	if got.Symbol.Qualified != s.Qualified {
		t.Errorf("Qualified = %q, want %q", got.Symbol.Qualified, s.Qualified)
	}
	if got.Symbol.Kind != s.Kind {
		t.Errorf("Kind = %q, want %q", got.Symbol.Kind, s.Kind)
	}
	if got.Symbol.LineStart != s.LineStart || got.Symbol.LineEnd != s.LineEnd {
		t.Errorf("lines = %d..%d, want %d..%d",
			got.Symbol.LineStart, got.Symbol.LineEnd, s.LineStart, s.LineEnd)
	}
	if got.Symbol.Docstring != s.Docstring {
		t.Errorf("Docstring = %q, want %q", got.Symbol.Docstring, s.Docstring)
	}
}

func testSymbolUpsertIdempotent(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileID := mustWriteFile(t, ctx, idx, sampleFile("app/services/billing.rb"))
	s := sampleSymbol(fileID, "charge", "BillingService#charge", model.KindMethod)
	id1 := mustWriteSymbol(t, ctx, idx, s)

	s2 := sampleSymbol(fileID, "charge", "BillingService#charge", model.KindMethod)
	s2.LineStart = 42
	id2 := mustWriteSymbol(t, ctx, idx, s2)

	if id1 != id2 {
		t.Errorf("upsert returned new id: first=%d second=%d", id1, id2)
	}
}

func testSymbolSameQualifiedDifferentFile(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileA := mustWriteFile(t, ctx, idx, sampleFile("app/legacy/user.rb"))
	fileB := mustWriteFile(t, ctx, idx, sampleFile("app/models/user.rb"))

	idA := mustWriteSymbol(t, ctx, idx, sampleSymbol(fileA, "User", "User", model.KindClass))
	idB := mustWriteSymbol(t, ctx, idx, sampleSymbol(fileB, "User", "User", model.KindClass))

	if idA == idB {
		t.Fatalf("two files with the same qualified name collapsed: id=%d", idA)
	}
}

func testSymbolNullablesRoundTrip(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileID := mustWriteFile(t, ctx, idx, sampleFile("app/models/nullable.rb"))

	// Write a parent symbol to hold a valid ParentID reference.
	parentID := mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "Nullable", "Nullable", model.KindClass))

	complexity := 7
	withValues := sampleSymbol(fileID, "withVals", "Nullable#withVals", model.KindMethod)
	withValues.ParentID = &parentID
	withValues.Complexity = &complexity
	id1 := mustWriteSymbol(t, ctx, idx, withValues)

	nilDefaults := sampleSymbol(fileID, "nilDefaults", "Nullable#nilDefaults", model.KindMethod)
	// ParentID and Complexity remain nil.
	id2 := mustWriteSymbol(t, ctx, idx, nilDefaults)

	g1, err := idx.ReadSymbol(ctx, id1)
	if err != nil {
		t.Fatalf("ReadSymbol(withValues): %v", err)
	}
	if g1.Symbol.ParentID == nil {
		t.Errorf("ParentID = nil, want %d", parentID)
	} else if *g1.Symbol.ParentID != parentID {
		t.Errorf("ParentID = %d, want %d", *g1.Symbol.ParentID, parentID)
	}
	if g1.Symbol.Complexity == nil {
		t.Errorf("Complexity = nil, want %d", complexity)
	} else if *g1.Symbol.Complexity != complexity {
		t.Errorf("Complexity = %d, want %d", *g1.Symbol.Complexity, complexity)
	}

	g2, err := idx.ReadSymbol(ctx, id2)
	if err != nil {
		t.Fatalf("ReadSymbol(nilDefaults): %v", err)
	}
	if g2.Symbol.ParentID != nil {
		t.Errorf("ParentID = %d, want nil", *g2.Symbol.ParentID)
	}
	if g2.Symbol.Complexity != nil {
		t.Errorf("Complexity = %d, want nil", *g2.Symbol.Complexity)
	}
}

func testEdgeRoundTrip(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileID := mustWriteFile(t, ctx, idx, sampleFile("app/controllers/orders_controller.rb"))
	srcID := mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "create", "OrdersController#create", model.KindMethod))
	tgtID := mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "call", "CheckoutService.call", model.KindMethod))

	// Use a non-default Confidence so an adapter that silently hardcodes 1.0
	// or zeroes Confidence on read is detected.
	e := sampleEdge(srcID, tgtID, fileID, model.EdgeCalls)
	e.Confidence = 0.7
	mustWriteEdge(t, ctx, idx, e)

	src, err := idx.ReadSymbol(ctx, srcID)
	if err != nil {
		t.Fatalf("ReadSymbol(source): %v", err)
	}
	if len(src.Outbound) != 1 {
		t.Fatalf("source.Outbound = %d edges, want 1", len(src.Outbound))
	}
	outbound := src.Outbound[0].Edge
	if outbound.Kind != model.EdgeCalls {
		t.Errorf("outbound kind = %q, want calls", outbound.Kind)
	}
	if src.Outbound[0].Target.ID != tgtID {
		t.Errorf("outbound target id = %d, want %d", src.Outbound[0].Target.ID, tgtID)
	}
	if outbound.Line == nil || *outbound.Line != 3 {
		t.Errorf("outbound line = %v, want 3", outbound.Line)
	}
	if outbound.Confidence != 0.7 {
		t.Errorf("outbound confidence = %v, want 0.7", outbound.Confidence)
	}

	tgt, err := idx.ReadSymbol(ctx, tgtID)
	if err != nil {
		t.Fatalf("ReadSymbol(target): %v", err)
	}
	if len(tgt.Inbound) != 1 {
		t.Fatalf("target.Inbound = %d edges, want 1", len(tgt.Inbound))
	}
	if tgt.Inbound[0].Target.ID != srcID {
		t.Errorf("inbound target id = %d, want %d", tgt.Inbound[0].Target.ID, srcID)
	}
}

func testEdgeUpsertIdempotent(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileID := mustWriteFile(t, ctx, idx, sampleFile("app/jobs/checkout_job.rb"))
	srcID := mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "perform", "CheckoutJob#perform", model.KindMethod))
	tgtID := mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "call", "CheckoutService.call", model.KindMethod))

	id1 := mustWriteEdge(t, ctx, idx, sampleEdge(srcID, tgtID, fileID, model.EdgeCalls))

	e2 := sampleEdge(srcID, tgtID, fileID, model.EdgeCalls)
	e2.Confidence = 0.7
	id2 := mustWriteEdge(t, ctx, idx, e2)

	if id1 != id2 {
		t.Errorf("upsert returned new id: first=%d second=%d", id1, id2)
	}
}

func testReadSymbolNotFound(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	// Deliberately high id that cannot plausibly have been allocated by an
	// autoincrement — more honest than a small magic number like 999999.
	const impossibleID int64 = 1 << 62

	got, err := idx.ReadSymbol(ctx, impossibleID)
	if !errors.Is(err, index.ErrNotFound) {
		t.Errorf("err = %v, want errors.Is(_, ErrNotFound)", err)
	}
	if got != nil {
		t.Errorf("got non-nil SymbolContext on not-found: %+v", got)
	}
}

func testQueryEmpty(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	got, err := idx.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty index returned %d rows", len(got))
	}
}

func testQueryByKind(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileID := mustWriteFile(t, ctx, idx, sampleFile("app/models/order.rb"))
	mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "Order", "Order", model.KindClass))
	mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "total", "Order#total", model.KindMethod))
	mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "finalize", "Order#finalize", model.KindMethod))

	got, err := idx.Query(ctx, index.Filter{Kind: model.KindMethod})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Query(Kind=method) = %d rows, want 2", len(got))
	}
	for _, s := range got {
		if s.Kind != model.KindMethod {
			t.Errorf("row has kind %q, want method", s.Kind)
		}
	}
}

func testQueryByFileID(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileA := mustWriteFile(t, ctx, idx, sampleFile("app/models/a.rb"))
	fileB := mustWriteFile(t, ctx, idx, sampleFile("app/models/b.rb"))
	mustWriteSymbol(t, ctx, idx, sampleSymbol(fileA, "A", "A", model.KindClass))
	mustWriteSymbol(t, ctx, idx, sampleSymbol(fileA, "x", "A#x", model.KindMethod))
	mustWriteSymbol(t, ctx, idx, sampleSymbol(fileB, "B", "B", model.KindClass))

	got, err := idx.Query(ctx, index.Filter{FileID: fileA})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Query(FileID=%d) = %d rows, want 2", fileA, len(got))
	}
	for _, s := range got {
		if s.FileID != fileA {
			t.Errorf("row FileID = %d, want %d", s.FileID, fileA)
		}
	}
}

func testQueryByName(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileID := mustWriteFile(t, ctx, idx, sampleFile("app/services/payments.rb"))
	mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "charge", "Payments#charge", model.KindMethod))
	mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "refund", "Payments#refund", model.KindMethod))

	got, err := idx.Query(ctx, index.Filter{Name: "charge"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].Name != "charge" {
		t.Fatalf("Query(Name=charge) = %+v, want single 'charge' row", got)
	}
}

func testQueryLimit(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileID := mustWriteFile(t, ctx, idx, sampleFile("app/models/many.rb"))
	for i := 0; i < 5; i++ {
		name := string(rune('a' + i))
		mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, name, "Many#"+name, model.KindMethod))
	}

	got, err := idx.Query(ctx, index.Filter{Limit: 3})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("Query(Limit=3) = %d rows, want 3", len(got))
	}
}

func testClear(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := open(t, newIdx)

	fileID := mustWriteFile(t, ctx, idx, sampleFile("app/models/temp.rb"))
	symID := mustWriteSymbol(t, ctx, idx, sampleSymbol(fileID, "Temp", "Temp", model.KindClass))

	if err := idx.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	got, err := idx.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query after Clear: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Query after Clear = %d rows, want 0", len(got))
	}

	if _, err := idx.ReadSymbol(ctx, symID); !errors.Is(err, index.ErrNotFound) {
		t.Errorf("ReadSymbol after Clear: err = %v, want ErrNotFound", err)
	}
}

func testClose(t *testing.T, newIdx Factory) {
	ctx := context.Background()
	idx := newIdx(t)
	// Not using the open helper here — we're exercising Close directly and
	// want to avoid a double-close via t.Cleanup.
	if _, err := idx.WriteFile(ctx, sampleFile("app/models/close.rb")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
