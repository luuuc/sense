// Package index defines the storage contract for the sense graph and the
// sentinel errors implementations return. The package deliberately imports
// only the model package and stdlib — concrete adapters (e.g.
// internal/sqlite) depend on index, not the other way round.
package index

import (
	"context"
	"errors"
	"io"

	"github.com/luuuc/sense/internal/model"
)

// ErrNotFound is returned when a read targets an id or filter that matches
// no row. Callers distinguish "missing" from other failures with errors.Is.
var ErrNotFound = errors.New("index: not found")

// Filter bounds a Query. Zero values mean "no constraint"; a zero Limit
// means "no limit". Name is matched exactly — substring and semantic search
// belong in later layers, not here.
type Filter struct {
	Name   string
	Kind   model.SymbolKind
	FileID int64
	Limit  int
}

// Index is the storage contract for sense's graph. Every implementation
// must pass the conformance suite in conformance.go and satisfy io.Closer
// so higher layers can release backing resources without type-asserting
// to a concrete adapter.
//
// Writes are upserts keyed by each type's natural unique key:
//
//	File   → Path
//	Symbol → (FileID, Qualified)
//	Edge   → (SourceID, TargetID, Kind, FileID)
//
// A second write with the same key updates the remaining fields and
// returns the existing row's ID. This keeps `sense scan` idempotent.
// The adapter is responsible for declaring the UNIQUE indexes that
// enforce these keys; the interface documents the contract but cannot
// enforce it alone.
//
// Reads return ErrNotFound when no row matches.
type Index interface {
	io.Closer

	WriteFile(ctx context.Context, f *model.File) (int64, error)
	WriteSymbol(ctx context.Context, s *model.Symbol) (int64, error)
	WriteEdge(ctx context.Context, e *model.Edge) (int64, error)
	ReadSymbol(ctx context.Context, id int64) (*model.SymbolContext, error)
	Query(ctx context.Context, f Filter) ([]model.Symbol, error)
	Clear(ctx context.Context) error
}
