// Package model defines the row-level types that mirror the sense index
// schema. Each type corresponds to a table in .doc/definition/03-data-model.md
// and is deliberately a dumb data carrier — the sqlite adapter owns scanning,
// higher layers own behavior.
package model

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
