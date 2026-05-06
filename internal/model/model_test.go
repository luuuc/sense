package model

import (
	"database/sql"
	"testing"
)

func TestInt64Ptr(t *testing.T) {
	p := Int64Ptr(42)
	if p == nil {
		t.Fatal("Int64Ptr returned nil")
	}
	if *p != 42 {
		t.Errorf("Int64Ptr(42) = %d", *p)
	}
}

func TestHydrateSymbolNullables_AllValid(t *testing.T) {
	s := &Symbol{}
	HydrateSymbolNullables(s,
		sql.NullInt64{Int64: 7, Valid: true},
		sql.NullInt64{Int64: 3, Valid: true},
		sql.NullString{String: "public", Valid: true},
		sql.NullString{String: "does things", Valid: true},
		sql.NullString{String: "func foo()", Valid: true},
	)
	if s.ParentID == nil || *s.ParentID != 7 {
		t.Errorf("ParentID = %v, want 7", s.ParentID)
	}
	if s.Complexity == nil || *s.Complexity != 3 {
		t.Errorf("Complexity = %v, want 3", s.Complexity)
	}
	if s.Visibility != "public" {
		t.Errorf("Visibility = %q, want public", s.Visibility)
	}
	if s.Docstring != "does things" {
		t.Errorf("Docstring = %q", s.Docstring)
	}
	if s.Snippet != "func foo()" {
		t.Errorf("Snippet = %q", s.Snippet)
	}
}

func TestHydrateSymbolNullables_AllNull(t *testing.T) {
	s := &Symbol{}
	HydrateSymbolNullables(s,
		sql.NullInt64{},
		sql.NullInt64{},
		sql.NullString{},
		sql.NullString{},
		sql.NullString{},
	)
	if s.ParentID != nil {
		t.Errorf("ParentID should be nil, got %v", s.ParentID)
	}
	if s.Complexity != nil {
		t.Errorf("Complexity should be nil, got %v", s.Complexity)
	}
	if s.Visibility != "" {
		t.Errorf("Visibility should be empty, got %q", s.Visibility)
	}
	if s.Docstring != "" {
		t.Errorf("Docstring should be empty, got %q", s.Docstring)
	}
	if s.Snippet != "" {
		t.Errorf("Snippet should be empty, got %q", s.Snippet)
	}
}

func TestHydrateSymbolNullables_PartiallyValid(t *testing.T) {
	s := &Symbol{}
	HydrateSymbolNullables(s,
		sql.NullInt64{Int64: 10, Valid: true},
		sql.NullInt64{},
		sql.NullString{String: "private", Valid: true},
		sql.NullString{},
		sql.NullString{String: "class Bar", Valid: true},
	)
	if s.ParentID == nil || *s.ParentID != 10 {
		t.Errorf("ParentID = %v, want 10", s.ParentID)
	}
	if s.Complexity != nil {
		t.Errorf("Complexity should be nil, got %v", s.Complexity)
	}
	if s.Visibility != "private" {
		t.Errorf("Visibility = %q, want private", s.Visibility)
	}
	if s.Snippet != "class Bar" {
		t.Errorf("Snippet = %q", s.Snippet)
	}
}
