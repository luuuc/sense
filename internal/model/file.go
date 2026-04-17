package model

import "time"

// File mirrors the sense_files table: one row per indexed source file.
type File struct {
	ID        int64
	Path      string
	Language  string
	Hash      string
	Symbols   int
	IndexedAt time.Time
}
