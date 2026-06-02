package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// The fault driver wraps the registered modernc "sqlite" driver and fails the
// first ExecContext whose SQL contains an armed substring. It exists to drive
// resetSenseTables / applySchema down their mid-sequence DB-error returns,
// which a healthy database never reaches and the closed-adapter test (an
// entry-point failure) cannot reach either.

var (
	faultOnce sync.Once
	faultMu   sync.Mutex
	faultSub  string // when non-empty, fail the first matching Exec
)

func armFault(sub string) {
	faultMu.Lock()
	faultSub = sub
	faultMu.Unlock()
}

func disarmFault() { armFault("") }

func matchFault(query string) bool {
	faultMu.Lock()
	defer faultMu.Unlock()
	if faultSub != "" && strings.Contains(query, faultSub) {
		faultSub = "" // one-shot: fire once, then let the sequence unwind
		return true
	}
	return false
}

type faultDriver struct{ base driver.Driver }

func (d *faultDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &faultConn{base: c}, nil
}

type faultConn struct{ base driver.Conn }

func (c *faultConn) Prepare(q string) (driver.Stmt, error) { return c.base.Prepare(q) }
func (c *faultConn) Close() error                          { return c.base.Close() }
func (c *faultConn) Begin() (driver.Tx, error)             { return c.base.Begin() } //nolint:staticcheck // delegating to base

func (c *faultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if matchFault(query) {
		return nil, fmt.Errorf("injected fault: exec %q", query)
	}
	ec, ok := c.base.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return ec.ExecContext(ctx, query, args)
}

func (c *faultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.base.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return qc.QueryContext(ctx, query, args)
}

// openFaultDB returns a *sql.DB on the fault driver with the schema applied,
// pinned to a single connection so the fault fires deterministically within
// one statement sequence.
func openFaultDB(t *testing.T) *sql.DB {
	t.Helper()
	faultOnce.Do(func() {
		probe, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			panic(err)
		}
		base := probe.Driver()
		_ = probe.Close()
		sql.Register("sqlite-fault", &faultDriver{base: base})
	})
	db, err := sql.Open("sqlite-fault", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open fault db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := applySchema(context.Background(), db); err != nil {
		t.Fatalf("apply schema on fault db: %v", err)
	}
	return db
}

// TestResetSenseTablesInjectedErrors drives every write step of the reset
// sequence to its error return by failing exactly that statement.
func TestResetSenseTablesInjectedErrors(t *testing.T) {
	cases := []struct {
		name    string
		trigger string
		wantErr string
	}{
		{"foreign_keys_off", "foreign_keys = OFF", "disable foreign_keys"},
		{"drop_table", "DROP TABLE", "sqlite drop"},
		{"user_version", "user_version = 0", "reset user_version"},
		{"base_schema", "CREATE TABLE IF NOT EXISTS sense_files", "sqlite schema"},
		{"fts_schema", "CREATE VIRTUAL TABLE", "fts schema"},
		{"fts_trigger", "sense_symbols_fts_insert", "fts trigger"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openFaultDB(t)
			ctx := context.Background()

			armFault(tc.trigger)
			defer disarmFault()

			err := resetSenseTables(ctx, db, metricsPreserve)
			if err == nil {
				t.Fatalf("resetSenseTables: expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestResetSenseTablesListTablesError covers the entry-point query failure:
// a closed connection makes the sqlite_master read fail before any drop.
func TestResetSenseTablesListTablesError(t *testing.T) {
	db := openFaultDB(t)
	_ = db.Close() // subsequent queries fail
	if err := resetSenseTables(context.Background(), db, metricsPreserve); err == nil {
		t.Fatal("resetSenseTables on closed db: expected error, got nil")
	}
}
