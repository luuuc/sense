package dead_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/scan"
)

// The fault driver wraps the modernc "sqlite" driver and fails the first
// QueryContext / QueryRowContext whose SQL contains an armed substring. It lets
// each FindDead/buildFacts error-propagation branch run by failing exactly the
// query that feeds it, while every earlier query in the pipeline still succeeds
// — something a closed DB or a dropped table cannot do, since the pipeline's
// queries all share the same two tables.

var (
	deadFaultOnce sync.Once
	deadFaultMu   sync.Mutex
	deadFaultSub  string
)

func armDeadFault(sub string) {
	deadFaultMu.Lock()
	deadFaultSub = sub
	deadFaultMu.Unlock()
}

func matchDeadFault(query string) bool {
	deadFaultMu.Lock()
	defer deadFaultMu.Unlock()
	if deadFaultSub != "" && strings.Contains(query, deadFaultSub) {
		deadFaultSub = "" // one-shot
		return true
	}
	return false
}

type deadFaultDriver struct{ base driver.Driver }

func (d *deadFaultDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &deadFaultConn{base: c}, nil
}

type deadFaultConn struct{ base driver.Conn }

func (c *deadFaultConn) Prepare(q string) (driver.Stmt, error) { return c.base.Prepare(q) }
func (c *deadFaultConn) Close() error                          { return c.base.Close() }

//nolint:staticcheck // driver.Conn requires the deprecated Begin method
func (c *deadFaultConn) Begin() (driver.Tx, error) { return c.base.Begin() }

func (c *deadFaultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.base.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return ec.ExecContext(ctx, query, args)
}

func (c *deadFaultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if matchDeadFault(query) {
		return nil, fmt.Errorf("injected fault: query %q", query)
	}
	qc, ok := c.base.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return qc.QueryContext(ctx, query, args)
}

// faultFixtureDB scans a small mixed-language project into a fresh index, then
// reopens that index on the fault driver. The project includes a dead Ruby
// class (so candidates/rollup/name-occurrence steps all do real work) and a Go
// main + interface (so the interface and main-function facts have rows).
func faultFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "live_service.rb"), `class LiveService
  def process
    42
  end
end
`)
	writeFile(t, filepath.Join(root, "dead_service.rb"), `class DeadService
  def handle
    1
  end
end
`)
	writeFile(t, filepath.Join(root, "caller.rb"), `class Caller
  def run
    send(:process)
  end
end
`)
	writeFile(t, filepath.Join(root, "main.go"), `package main

func main() {}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")

	deadFaultOnce.Do(func() {
		probe, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			panic(err)
		}
		base := probe.Driver()
		_ = probe.Close()
		sql.Register("sqlite-deadfault", &deadFaultDriver{base: base})
	})

	db, err := sql.Open("sqlite-deadfault", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open fault db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestFindDeadQueryFaultsPropagate(t *testing.T) {
	cases := []struct {
		name    string
		trigger string // unique substring of the query whose failure we force
	}{
		{"queryCandidates", "%.d.ts"},
		{"interfaceAliveMethods", "iface.kind = 'interface'"},
		{"queryTestsTargets", "kind = 'tests'"},
		{"findLiveContainers", "SELECT parent_id, id FROM sense_symbols"},
		{"buildFacts_valueObjects", "t.qualified IN"},
		{"buildFacts_includedModules", "kind = 'includes' AND target_id IS NOT NULL"},
		{"buildFacts_controllerConcerns", "LIKE '%Controller'"},
		{"buildFacts_interfaceMethodNames", "p.kind = 'interface'"},
		{"populateNameOccurrences", "GROUP BY name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := faultFixtureDB(t)
			armDeadFault(tc.trigger)
			t.Cleanup(func() { armDeadFault("") })

			_, err := dead.FindDead(context.Background(), db, dead.Options{})
			if err == nil {
				t.Fatalf("FindDead: expected error when %s query fails, got nil", tc.name)
			}
		})
	}
}
