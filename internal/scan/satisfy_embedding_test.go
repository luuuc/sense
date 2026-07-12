package scan_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/scan"
)

// TestScan_SatisfyViaEmbeddedMethod proves the embedded-method promotion
// path of interface satisfaction: a struct that declares no methods of its
// own but embeds another struct whose method set satisfies an interface must
// still earn the inherits edge. This is the only path that exercises
// promoteEmbeddedMethodSets (loading the includes edges and walking them) and
// the recursive promoteEmbeddedMethods hop that finds the embedded struct in
// the struct map — the plain "struct declares the methods directly" fixture
// never touches it.
func TestScan_SatisfyViaEmbeddedMethod(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "reader.go"), `package mylib

type Reader interface {
	Read() string
}
`)

	// Base declares Read; Derived embeds Base and declares nothing. Derived
	// satisfies Reader only after Base's Read is promoted across the embedding.
	writeFile(t, filepath.Join(root, "impl.go"), `package mylib

type Base struct{}

func (b *Base) Read() string { return "base" }

type Derived struct {
	Base
}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Derived → Reader inherits edge must exist purely via the embedded method.
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM sense_edges e
		JOIN sense_symbols s ON s.id = e.source_id
		JOIN sense_symbols t ON t.id = e.target_id
		WHERE e.kind = 'inherits'
		  AND s.name = 'Derived'
		  AND t.name = 'Reader'`).Scan(&count)
	if err != nil {
		t.Fatalf("query inherits edge: %v", err)
	}
	if count == 0 {
		t.Error("expected Derived → Reader inherits edge via promoted embedded method")
	}
}

// TestScan_SatisfyCompositeInterface proves the G-1 false-negative fix: an
// interface built ONLY from embedded interfaces (the hugo page.Page shape)
// must still be satisfiable — pre-fix its direct method set was empty and the
// whole interface was skipped.
func TestScan_SatisfyCompositeInterface(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "iface.go"), `package mylib

type Reader interface {
	Read() string
}

type Closer interface {
	Close() error
}

type ReadCloser interface {
	Reader
	Closer
}
`)
	writeFile(t, filepath.Join(root, "impl.go"), `package mylib

type File struct{}

func (f *File) Read() string { return "" }
func (f *File) Close() error { return nil }
`)

	if n := queryInherits(t, root, "File", "ReadCloser"); n == 0 {
		t.Error("expected File → ReadCloser inherits edge via composite-interface expansion")
	}
}

// TestScan_NoSatisfyOnPartialCover proves the G-1 false-positive fix (the
// containerd ExecProcess shape): an interface with a big embedded set and one
// direct method must NOT be satisfied by a type that has only the direct
// method — pre-fix the required set collapsed to the direct method alone.
func TestScan_NoSatisfyOnPartialCover(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "iface.go"), `package mylib

type Process interface {
	Start() error
	Stop() error
}

type ExecProcess interface {
	Process
	Delete() error
}
`)
	writeFile(t, filepath.Join(root, "impl.go"), `package mylib

type Store struct{}

func (s *Store) Delete() error { return nil }
`)

	if n := queryInherits(t, root, "Store", "ExecProcess"); n != 0 {
		t.Error("Store must NOT satisfy ExecProcess with only the direct method covered")
	}
}

// TestScan_SatisfyViaEmbeddedInterfaceValue proves the struct-side fix (the
// hugo pageState shape): a struct that embeds an interface VALUE delegates
// the interface's whole method set and satisfies it — pre-fix promotion only
// walked embedded structs.
func TestScan_SatisfyViaEmbeddedInterfaceValue(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "iface.go"), `package mylib

type Pager interface {
	Title() string
	Kind() string
}
`)
	writeFile(t, filepath.Join(root, "impl.go"), `package mylib

type wrapper struct {
	Pager
}
`)

	if n := queryInherits(t, root, "wrapper", "Pager"); n == 0 {
		t.Error("expected wrapper → Pager inherits edge via embedded interface value")
	}
}

// TestScan_SatisfyPins pins three behaviors the fix must NOT change: a NAMED
// interface field does not promote (Go semantics), an empty interface earns
// no edges (everything satisfies it — noise), and a composite of only
// unresolvable stdlib interfaces stays at zero edges (the recorded residual).
func TestScan_SatisfyPins(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "iface.go"), `package mylib

import (
	"fmt"
	"io"
)

type Pager interface {
	Title() string
}

type Any interface{}

type StdOnly interface {
	fmt.Stringer
	io.Closer
}
`)
	writeFile(t, filepath.Join(root, "impl.go"), `package mylib

type named struct {
	p Pager
}

type stringerCloser struct{}

func (s *stringerCloser) String() string { return "" }
func (s *stringerCloser) Close() error   { return nil }
`)

	if n := queryInherits(t, root, "named", "Pager"); n != 0 {
		t.Error("a NAMED interface field must not satisfy the interface")
	}
	if n := queryInherits(t, root, "named", "Any"); n != 0 {
		t.Error("interface{} must earn no satisfaction edges")
	}
	if n := queryInherits(t, root, "stringerCloser", "StdOnly"); n != 0 {
		t.Error("stdlib-only composite stays at zero edges (recorded residual)")
	}
}

// queryInherits scans root and counts inherits edges source→target by name.
func queryInherits(t *testing.T, root, source, target string) int {
	t.Helper()
	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM sense_edges e
		JOIN sense_symbols s ON s.id = e.source_id
		JOIN sense_symbols t ON t.id = e.target_id
		WHERE e.kind = 'inherits'
		  AND s.name = ?
		  AND t.name = ?`, source, target).Scan(&count)
	if err != nil {
		t.Fatalf("query inherits edge: %v", err)
	}
	return count
}
