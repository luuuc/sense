// Package model defines the row-level types that mirror the sense index
// schema. Each type corresponds to a table in .doc/definition/03-data-model.md
// and is deliberately a dumb data carrier — the sqlite adapter owns scanning,
// higher layers own behavior.
package model

import "database/sql"

// Symbol mirrors the sense_symbols table: one row per named entity.
//
// ParentID and Complexity are pointers because the underlying columns are
// nullable — distinguishing "no parent" from "parent is row 0".
type Symbol struct {
	ID         int64
	FileID     int64
	Name       string
	Qualified  string
	Kind       SymbolKind
	Visibility string
	ParentID   *int64
	LineStart  int
	LineEnd    int
	Docstring  string
	Complexity *int
	Snippet    string
}

// SymbolRef is the minimal shape used by resolution passes that need
// to look up symbols by qualified name without the full row payload
// (snippet, docstring, visibility). A `SELECT id, qualified, file_id`
// is an order of magnitude cheaper than loading whole Symbol rows
// when the caller is building an in-memory name index.
type SymbolRef struct {
	ID        int64
	Qualified string
	FileID    int64
}

// HydrateSymbolNullables copies the sql.NullXxx carriers scanned from
// sense_symbols onto s. Lives here so every consumer that hydrates
// symbol rows (the sqlite adapter, the blast engine, any future
// read-path) goes through one definition — adding a nullable column
// to the schema then means updating one hydrate function, not three.
func HydrateSymbolNullables(
	s *Symbol,
	parentID sql.NullInt64,
	complexity sql.NullInt64,
	visibility sql.NullString,
	docstring sql.NullString,
	snippet sql.NullString,
) {
	if parentID.Valid {
		p := parentID.Int64
		s.ParentID = &p
	}
	if complexity.Valid {
		c := int(complexity.Int64)
		s.Complexity = &c
	}
	s.Visibility = visibility.String
	s.Docstring = docstring.String
	s.Snippet = snippet.String
}

// SymbolKind enumerates the symbol categories the schema recognises.
// See 03-data-model.md for the canonical list.
type SymbolKind string

const (
	KindClass     SymbolKind = "class"
	KindModule    SymbolKind = "module"
	KindMethod    SymbolKind = "method"
	KindFunction  SymbolKind = "function"
	KindConstant  SymbolKind = "constant"
	KindInterface SymbolKind = "interface"
	KindType      SymbolKind = "type"
)
