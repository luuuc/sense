package blast

// Fault-injection coverage for the retention closure's error returns: every
// SQL helper propagates query failures, row-scan failures, and iteration
// failures up through loadRetention. A healthy database never reaches these
// branches; the fault driver (symbols_faults_test.go) forces them.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// openRetentionFaultDB seeds the minimal laundering shape (subject, carrier,
// rare interface + member, holder) on the fault driver so every retention
// query executes.
func openRetentionFaultDB(t *testing.T) (*sql.DB, model.Symbol, []int64) {
	t.Helper()
	return openRetentionFaultDBSeeded(t, false)
}

// openRetentionFaultDBSeeded is openRetentionFaultDB with a switch for the
// purity path: withTestSatisfier adds a second satisfier declared in a test
// file, so the refusal (and the name hydration it triggers) actually runs and
// its failure modes can be armed.
func openRetentionFaultDBSeeded(t *testing.T, withTestSatisfier bool) (*sql.DB, model.Symbol, []int64) {
	t.Helper()
	ctx := context.Background()
	dbPath := t.TempDir() + "/retention.db"
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	var subject model.Symbol
	var composerIDs []int64
	var testSatisfierID int64
	err = adapter.InTx(ctx, func() error {
		f, err := adapter.WriteFile(ctx, &model.File{
			Path: "widget.go", Language: "go", Hash: "h1", Symbols: 5, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		write := func(name string, kind model.SymbolKind, parent *int64) int64 {
			id, werr := adapter.WriteSymbol(ctx, &model.Symbol{
				FileID: f, Name: name, Qualified: name, Kind: kind,
				ParentID: parent, LineStart: 1, LineEnd: 2,
			})
			if werr != nil {
				err = werr
			}
			return id
		}
		subjectID := write("Widget", model.KindClass, nil)
		carrier := write("Carrier", model.KindClass, nil)
		iface := write("RareIface", model.KindInterface, nil)
		write("VisitRareThing", model.KindMethod, &iface)
		holder := write("Holder", model.KindClass, nil)
		if err != nil {
			return err
		}
		edge := func(src, tgt int64, kind model.EdgeKind) {
			if _, werr := adapter.WriteEdge(ctx, &model.Edge{
				SourceID: &src, TargetID: tgt, Kind: kind, FileID: f, Confidence: 0.9,
			}); werr != nil {
				err = werr
			}
		}
		edge(carrier, subjectID, model.EdgeComposes)
		edge(carrier, iface, model.EdgeInherits)
		edge(holder, iface, model.EdgeComposes)
		if withTestSatisfier {
			tf, ferr := adapter.WriteFile(ctx, &model.File{
				Path: "widget_test.go", Language: "go", Hash: "h2", Symbols: 1, IndexedAt: time.Now().UTC(),
			})
			if ferr != nil {
				return ferr
			}
			fake, werr := adapter.WriteSymbol(ctx, &model.Symbol{
				FileID: tf, Name: "FakeCarrier", Qualified: "FakeCarrier",
				Kind: model.KindClass, LineStart: 1, LineEnd: 2,
			})
			if werr != nil {
				return werr
			}
			edge(fake, subjectID, model.EdgeComposes)
			edge(fake, iface, model.EdgeInherits)
			// The closure's level 1 is the caller-supplied composer list (in
			// production, inboundComposers finds it), so the fake must be in
			// it or purity has nothing to refuse.
			testSatisfierID = fake
		}
		if err != nil {
			return err
		}
		subject = model.Symbol{ID: subjectID, Name: "Widget", Qualified: "Widget", Kind: model.KindClass, FileID: f}
		composerIDs = []int64{carrier}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if testSatisfierID != 0 {
		composerIDs = append(composerIDs, testSatisfierID)
	}

	blastFaultOnce.Do(func() {
		probe, perr := sql.Open("sqlite", ":memory:")
		if perr != nil {
			panic(perr)
		}
		base := probe.Driver()
		_ = probe.Close()
		sql.Register("sqlite-blastfault", &blastFaultDriver{base: base})
	})
	db, err := sql.Open("sqlite-blastfault", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open fault db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db, subject, composerIDs
}

func runRetention(t *testing.T, db *sql.DB, subject model.Symbol, composerIDs []int64) error {
	t.Helper()
	noSelf := func(model.Symbol) bool { return false }
	_, err := loadRetention(context.Background(), db, subject, []int64{subject.ID},
		composerIDs, map[int64]struct{}{}, map[int64]struct{}{}, noSelf, retentionPage{limit: 100})
	return err
}

// TestRetentionPropagatesQueryFaults arms an outright query failure on each
// distinct retention query and expects loadRetention to surface it.
func TestRetentionPropagatesQueryFaults(t *testing.T) {
	cases := map[string]string{
		"level1_embedders":    `e.kind IN ('includes')`,
		"level1_kind_filter":  `AND kind != 'interface'`,
		"deeper_levels":       `e.kind IN ('composes','includes')`,
		"forward_interfaces":  `e.kind = 'inherits'`,
		"sole_members":        `AND kind = 'method'`,
		"name_frequency":      `GROUP BY name`,
		"hydration":           `docstring, complexity, snippet`,
		"via_satisfier_count": `COUNT(DISTINCT e.source_id)`,
	}
	for name, sub := range cases {
		t.Run(name, func(t *testing.T) {
			db, subject, composerIDs := openRetentionFaultDB(t)
			armBlastQueryFault(sub)
			t.Cleanup(disarmBlastFaults)
			if err := runRetention(t, db, subject, composerIDs); err == nil {
				t.Fatalf("expected propagated query error for %s", name)
			}
		})
	}
}

// TestRetentionPropagatesRowFaults drives the row-scan and rows.Err branches
// of the chunked query helpers.
func TestRetentionPropagatesRowFaults(t *testing.T) {
	cases := map[string]struct {
		sub  string
		kind blastRowsFaultMode
	}{
		"pair_scan":      {`e.kind IN ('composes','includes')`, blastRowsBadValue},
		"pair_iteration": {`e.kind IN ('composes','includes')`, blastRowsNext},
		"member_scan":    {`AND kind = 'method'`, blastRowsScan},
		"member_iter":    {`AND kind = 'method'`, blastRowsNext},
		"freq_scan":      {`GROUP BY name`, blastRowsScan},
		"freq_iter":      {`GROUP BY name`, blastRowsNext},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			db, subject, composerIDs := openRetentionFaultDB(t)
			armBlastRowsFault(c.sub, c.kind)
			t.Cleanup(disarmBlastFaults)
			if err := runRetention(t, db, subject, composerIDs); err == nil {
				t.Fatalf("expected propagated rows error for %s", name)
			}
		})
	}
}

// TestRetentionCancelledContext: a context cancelled between the level-1
// fetch and the fixpoint loop surfaces as the loop's cancellation error.
func TestRetentionCancelledContext(t *testing.T) {
	db, subject, composerIDs := openRetentionFaultDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	noSelf := func(model.Symbol) bool { return false }
	_, err := loadRetention(ctx, db, subject, []int64{subject.ID},
		composerIDs, map[int64]struct{}{}, map[int64]struct{}{}, noSelf, retentionPage{limit: 100})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

// TestEdgeTableGroupsPropagateFaults drives the three error wraps in
// loadEdgeTableGroups: the shared composer fetch, the composition group's
// hydration, and the retention closure.
func TestEdgeTableGroupsPropagateFaults(t *testing.T) {
	cases := map[string]string{
		"composer_fetch":      `kind = 'composes'`,
		"composition_hydrate": `docstring, complexity, snippet`,
		"retention":           `e.kind IN ('includes')`,
	}
	for name, sub := range cases {
		t.Run(name, func(t *testing.T) {
			db, subject, _ := openRetentionFaultDB(t)
			armBlastQueryFault(sub)
			t.Cleanup(disarmBlastFaults)
			s := &bfsState{childSet: map[int64]struct{}{}}
			noSelf := func(model.Symbol) bool { return false }
			_, _, err := s.loadEdgeTableGroups(context.Background(), db, subject,
				[]int64{subject.ID}, map[int64]struct{}{subject.ID: {}}, nil, nil, noSelf, Options{MaxResults: 100})
			if err == nil {
				t.Fatalf("expected propagated error for %s", name)
			}
		})
	}
}

// TestComputePropagatesEdgeTableGroupFault covers Compute's error return for
// the edge-table-group stage end to end.
func TestComputePropagatesEdgeTableGroupFault(t *testing.T) {
	db, subject, _ := openRetentionFaultDB(t)
	armBlastQueryFault(`kind = 'composes'`)
	t.Cleanup(disarmBlastFaults)
	if _, err := Compute(context.Background(), db, []int64{subject.ID}, Options{MaxHops: 1}); err == nil {
		t.Fatal("Compute: expected propagated edge-table-group error, got nil")
	}
}

// TestRetentionExcludedNamesPropagateFault: the purity refusal hydrates the
// refused satisfiers' names, and a failure there must surface rather than
// yield a ring whose disclosure silently lost its names.
func TestRetentionExcludedNamesPropagateFault(t *testing.T) {
	db, subject, composerIDs := openRetentionFaultDBSeeded(t, true)
	armBlastQueryFault(`docstring, complexity, snippet`)
	t.Cleanup(disarmBlastFaults)

	noSelf := func(model.Symbol) bool { return false }
	_, err := loadRetention(context.Background(), db, subject, []int64{subject.ID},
		composerIDs, map[int64]struct{}{}, map[int64]struct{}{}, noSelf, retentionPage{limit: 100})
	if err == nil {
		t.Fatal("expected the excluded-satisfier hydration fault to propagate")
	}
}

// TestRetentionFingerprintDegradesOnFault: the page fingerprint is
// best-effort. An unreadable index generation yields an EMPTY fingerprint,
// never a failed blast and never a fabricated stamp that would let two
// incomparable pages look unionable.
func TestRetentionFingerprintDegradesOnFault(t *testing.T) {
	db, subject, composerIDs := openRetentionFaultDB(t)
	armBlastQueryFault(`MAX(indexed_at)`)
	t.Cleanup(disarmBlastFaults)

	noSelf := func(model.Symbol) bool { return false }
	out, err := loadRetention(context.Background(), db, subject, []int64{subject.ID},
		composerIDs, map[int64]struct{}{}, map[int64]struct{}{}, noSelf, retentionPage{limit: 100})
	if err != nil {
		t.Fatalf("a fingerprint fault must not fail the blast: %v", err)
	}
	if out.fingerprint != "" {
		t.Errorf("fingerprint = %q, want empty when the generation is unreadable", out.fingerprint)
	}
	if len(out.holders) == 0 {
		t.Error("the ring itself must survive a fingerprint fault")
	}
}
