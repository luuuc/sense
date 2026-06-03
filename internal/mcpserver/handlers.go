package mcpserver

import (
	"database/sql"
	"sync"

	"github.com/luuuc/sense/internal/freshen"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

// handlers holds shared state for MCP tool handlers. The adapter is
// kept for methods like ReadSymbol that live on *sqlite.Adapter; db
// is a convenience alias for plain-SQL callers (Lookup, LoadFilePaths).
// Its methods are spread across the concern files in this package
// (graph.go, search.go, blast.go, conventions.go, status.go); this is
// the one explicit home for the type they all hang off.
type handlers struct {
	adapter      *sqlite.Adapter
	db           *sql.DB
	dir          string
	search       *search.Engine
	textFallback *search.TextFallback
	watchState   *mcpio.WatchState
	freshen      *freshen.Service // nil unless this server hosts the writer
	tracker      *metrics.Tracker
	defaults     profile.Defaults
	seenSymbols  map[int64]bool
	seenMu       sync.Mutex

	symbolCountOnce sync.Once
	symbolCountVal  int
}
