package mcpio

import (
	"sync"
	"time"
)

// WatchState is a thread-safe container for watch mode status. It is
// shared between the freshening Service (writer) and the MCP server
// (reader), so the server can report live watcher state without a
// package-level global.
type WatchState struct {
	mu          sync.RWMutex
	on          bool
	since       time.Time
	lastIndexed time.Time // time of the most recent background re-index
	pending     int       // files seen but not yet embedded (embedding debt)
}

// Set updates the watching flag and start time.
func (ws *WatchState) Set(watching bool, since time.Time) {
	ws.mu.Lock()
	ws.on = watching
	ws.since = since
	ws.mu.Unlock()
}

// SetIndexed records that a background re-index completed at the given
// time, leaving pending embeddings outstanding.
func (ws *WatchState) SetIndexed(at time.Time, pending int) {
	ws.mu.Lock()
	ws.lastIndexed = at
	ws.pending = pending
	ws.mu.Unlock()
}

// Get returns the watching flag and start time.
func (ws *WatchState) Get() (watching bool, since time.Time) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.on, ws.since
}

// Snapshot returns the full watch state in one read: whether watching is
// active, when it started, when it last re-indexed, and how many symbols
// are pending embeddings.
func (ws *WatchState) Snapshot() (watching bool, since, lastIndexed time.Time, pending int) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.on, ws.since, ws.lastIndexed, ws.pending
}
