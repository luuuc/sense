package blast

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// The fault driver wraps the modernc "sqlite" driver to drive the DB-error
// returns in loadSymbols and SiblingSymbolIDs — a query that fails outright,
// and a query that succeeds but whose row iteration misbehaves (Scan column
// mismatch, or a non-EOF Next error surfaced by rows.Err). A healthy database
// never reaches these branches.

type blastRowsFaultMode int

const (
	blastRowsNone     blastRowsFaultMode = iota
	blastRowsScan                        // report one column so the 12-target Scan fails
	blastRowsNext                        // return a non-EOF error from Next, surfaced by rows.Err
	blastRowsBadValue                    // return a value the int64 destination rejects
)

var (
	blastFaultOnce sync.Once
	blastFaultMu   sync.Mutex
	blastQuerySub  string // fail the first matching QueryContext outright
	blastRowsSub   string // the first matching query returns faulty rows
	blastRowsKind  blastRowsFaultMode
)

func armBlastQueryFault(sub string) {
	blastFaultMu.Lock()
	blastQuerySub = sub
	blastFaultMu.Unlock()
}

func armBlastRowsFault(sub string, kind blastRowsFaultMode) {
	blastFaultMu.Lock()
	blastRowsSub = sub
	blastRowsKind = kind
	blastFaultMu.Unlock()
}

func disarmBlastFaults() {
	blastFaultMu.Lock()
	blastQuerySub, blastRowsSub, blastRowsKind = "", "", blastRowsNone
	blastFaultMu.Unlock()
}

func matchBlastQueryFault(q string) bool {
	blastFaultMu.Lock()
	defer blastFaultMu.Unlock()
	if blastQuerySub != "" && strings.Contains(q, blastQuerySub) {
		blastQuerySub = ""
		return true
	}
	return false
}

func matchBlastRowsFault(q string) (blastRowsFaultMode, bool) {
	blastFaultMu.Lock()
	defer blastFaultMu.Unlock()
	if blastRowsSub != "" && strings.Contains(q, blastRowsSub) {
		kind := blastRowsKind
		blastRowsSub, blastRowsKind = "", blastRowsNone
		return kind, true
	}
	return blastRowsNone, false
}

type blastFaultDriver struct{ base driver.Driver }

func (d *blastFaultDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &blastFaultConn{base: c}, nil
}

type blastFaultConn struct{ base driver.Conn }

func (c *blastFaultConn) Prepare(q string) (driver.Stmt, error) { return c.base.Prepare(q) }
func (c *blastFaultConn) Close() error                          { return c.base.Close() }

//nolint:staticcheck // driver.Conn requires the deprecated Begin method
func (c *blastFaultConn) Begin() (driver.Tx, error) { return c.base.Begin() }

func (c *blastFaultConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.base.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return ec.ExecContext(ctx, q, args)
}

func (c *blastFaultConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if matchBlastQueryFault(q) {
		return nil, fmt.Errorf("injected fault: query %q", q)
	}
	if kind, armed := matchBlastRowsFault(q); armed {
		return &blastFaultRows{mode: kind}, nil
	}
	qc, ok := c.base.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return qc.QueryContext(ctx, q, args)
}

type blastFaultRows struct {
	mode blastRowsFaultMode
	done bool
}

func (r *blastFaultRows) Columns() []string {
	if r.mode == blastRowsScan {
		// One column against a multi-target Scan: database/sql rejects the
		// destination count, hitting the Scan error return.
		return []string{"id"}
	}
	return []string{"id"}
}

func (r *blastFaultRows) Close() error { return nil }

func (r *blastFaultRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	switch r.mode {
	case blastRowsNext:
		return fmt.Errorf("injected fault: rows iteration")
	case blastRowsBadValue:
		// A string against an int64 Scan destination triggers the row Scan
		// error return in SiblingSymbolIDs.
		dest[0] = "not-an-int"
		return nil
	default:
		dest[0] = int64(1)
		return nil
	}
}

func openBlastFaultDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	// Seed a real schema + two sibling symbols via the adapter on a temp-file
	// DB, then reopen that same file on the fault driver.
	dbPath := t.TempDir() + "/index.db"
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	err = adapter.InTx(ctx, func() error {
		f, err := adapter.WriteFile(ctx, &model.File{
			Path: "widget.rb", Language: "ruby", Hash: "h1", Symbols: 1, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		if _, err := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: f, Name: "Widget", Qualified: "Widget", Kind: model.KindClass, LineStart: 1, LineEnd: 10,
		}); err != nil {
			return err
		}
		_, err = adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: f, Name: "Widget", Qualified: "Widget", Kind: model.KindClass, LineStart: 20, LineEnd: 30,
		})
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	blastFaultOnce.Do(func() {
		probe, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			panic(err)
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
	return db
}

func TestLoadSymbolsScanError(t *testing.T) {
	db := openBlastFaultDB(t)
	armBlastRowsFault("FROM sense_symbols", blastRowsScan)
	t.Cleanup(disarmBlastFaults)
	if _, err := loadSymbols(context.Background(), db, []int64{1, 2}); err == nil {
		t.Fatal("loadSymbols: expected scan error from faulty rows, got nil")
	}
}

func TestLoadSymbolsRowsIterationError(t *testing.T) {
	db := openBlastFaultDB(t)
	armBlastRowsFault("FROM sense_symbols", blastRowsNext)
	t.Cleanup(disarmBlastFaults)
	if _, err := loadSymbols(context.Background(), db, []int64{1, 2}); err == nil {
		t.Fatal("loadSymbols: expected rows.Err iteration error, got nil")
	}
}

func TestSiblingSymbolIDsQueryError(t *testing.T) {
	db := openBlastFaultDB(t)
	armBlastQueryFault("JOIN sense_symbols s2")
	t.Cleanup(disarmBlastFaults)
	if _, err := SiblingSymbolIDs(context.Background(), db, 1); err == nil {
		t.Fatal("SiblingSymbolIDs: expected query error, got nil")
	}
}

func TestSiblingSymbolIDsScanError(t *testing.T) {
	db := openBlastFaultDB(t)
	armBlastRowsFault("JOIN sense_symbols s2", blastRowsBadValue)
	t.Cleanup(disarmBlastFaults)
	if _, err := SiblingSymbolIDs(context.Background(), db, 1); err == nil {
		t.Fatal("SiblingSymbolIDs: expected row scan error, got nil")
	}
}

func TestInboundComposersQueryError(t *testing.T) {
	db := openBlastFaultDB(t)
	armBlastQueryFault("kind = 'composes'")
	t.Cleanup(disarmBlastFaults)
	if _, err := inboundComposers(context.Background(), db, []int64{1, 2}); err == nil {
		t.Fatal("inboundComposers: expected query error, got nil")
	}
}

func TestInboundComposersScanError(t *testing.T) {
	db := openBlastFaultDB(t)
	armBlastRowsFault("kind = 'composes'", blastRowsBadValue)
	t.Cleanup(disarmBlastFaults)
	if _, err := inboundComposers(context.Background(), db, []int64{1, 2}); err == nil {
		t.Fatal("inboundComposers: expected row scan error, got nil")
	}
}

func TestInboundComposersRowsIterationError(t *testing.T) {
	db := openBlastFaultDB(t)
	armBlastRowsFault("kind = 'composes'", blastRowsNext)
	t.Cleanup(disarmBlastFaults)
	if _, err := inboundComposers(context.Background(), db, []int64{1, 2}); err == nil {
		t.Fatal("inboundComposers: expected rows.Err iteration error, got nil")
	}
}

// TestLoadReverseCompositionPropagatesQueryError covers the error return after
// inboundComposers fails inside the method.
func TestLoadReverseCompositionPropagatesQueryError(t *testing.T) {
	db := openBlastFaultDB(t)
	armBlastQueryFault("kind = 'composes'")
	t.Cleanup(disarmBlastFaults)
	s := &bfsState{childSet: map[int64]struct{}{}}
	noSelf := func(model.Symbol) bool { return false }
	_, err := s.loadReverseComposition(context.Background(), db, []int64{1, 2},
		map[int64]struct{}{}, map[int64]struct{}{}, noSelf, 100)
	if err == nil {
		t.Fatal("loadReverseComposition: expected propagated query error, got nil")
	}
}
