package metrics

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Tracker accumulates per-query savings estimates into session (in-memory)
// and lifetime (SQLite-backed) counters. Session counters reset on process
// start. Lifetime counters flush to the sense_metrics table periodically
// and on graceful shutdown.
type Tracker struct {
	db            *sql.DB
	flushInterval time.Duration

	session struct {
		queries          atomic.Int64
		fileReadsAvoided atomic.Int64
		tokensSaved      atomic.Int64
	}

	topQuery struct {
		mu          sync.Mutex
		tool        string
		args        string
		tokensSaved int
	}

	lifetime struct {
		mu               sync.Mutex
		queries          int64
		fileReadsAvoided int64
		tokensSaved      int64
	}

	stop chan struct{}
	done chan struct{}
}

// NewTracker creates a tracker that flushes lifetime counters to db every 30s.
// Call Close to flush final counters and stop the background goroutine.
func NewTracker(db *sql.DB) *Tracker {
	return NewTrackerWithInterval(db, 30*time.Second)
}

// NewTrackerWithInterval creates a tracker with a custom flush interval.
func NewTrackerWithInterval(db *sql.DB, interval time.Duration) *Tracker {
	t := &Tracker{
		db:            db,
		flushInterval: interval,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	go t.flushLoop()
	return t
}

// Record adds a query's estimates to both session and lifetime counters.
func (t *Tracker) Record(tool, args string, fileReadsAvoided, tokensSaved int) {
	t.session.queries.Add(1)
	t.session.fileReadsAvoided.Add(int64(fileReadsAvoided))
	t.session.tokensSaved.Add(int64(tokensSaved))

	t.lifetime.mu.Lock()
	t.lifetime.queries++
	t.lifetime.fileReadsAvoided += int64(fileReadsAvoided)
	t.lifetime.tokensSaved += int64(tokensSaved)
	t.lifetime.mu.Unlock()

	t.topQuery.mu.Lock()
	if tokensSaved > t.topQuery.tokensSaved {
		t.topQuery.tool = tool
		t.topQuery.args = args
		t.topQuery.tokensSaved = tokensSaved
	}
	t.topQuery.mu.Unlock()
}

// Session returns current session counters.
func (t *Tracker) Session() SessionCounters {
	return SessionCounters{
		Queries:          int(t.session.queries.Load()),
		FileReadsAvoided: int(t.session.fileReadsAvoided.Load()),
		TokensSaved:      int(t.session.tokensSaved.Load()),
	}
}

// TopQuery returns the highest-saving query this session.
func (t *Tracker) TopQuery() *TopQueryInfo {
	t.topQuery.mu.Lock()
	defer t.topQuery.mu.Unlock()
	if t.topQuery.tokensSaved == 0 {
		return nil
	}
	return &TopQueryInfo{
		Tool:        t.topQuery.tool,
		Args:        t.topQuery.args,
		TokensSaved: t.topQuery.tokensSaved,
	}
}

// Lifetime returns lifetime counters (flushed + pending).
func (t *Tracker) Lifetime(ctx context.Context) LifetimeCounters {
	persisted := t.loadPersisted(ctx)

	t.lifetime.mu.Lock()
	pending := LifetimeCounters{
		Queries:          persisted.Queries + int(t.lifetime.queries),
		FileReadsAvoided: persisted.FileReadsAvoided + int(t.lifetime.fileReadsAvoided),
		TokensSaved:      persisted.TokensSaved + int(t.lifetime.tokensSaved),
	}
	t.lifetime.mu.Unlock()
	return pending
}

// Close flushes pending lifetime counters and stops the background goroutine.
func (t *Tracker) Close() {
	close(t.stop)
	<-t.done
	t.flush()
}

func (t *Tracker) flushLoop() {
	defer close(t.done)
	ticker := time.NewTicker(t.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.flush()
		case <-t.stop:
			return
		}
	}
}

func (t *Tracker) flush() {
	t.lifetime.mu.Lock()
	q := t.lifetime.queries
	f := t.lifetime.fileReadsAvoided
	ts := t.lifetime.tokensSaved
	t.lifetime.queries = 0
	t.lifetime.fileReadsAvoided = 0
	t.lifetime.tokensSaved = 0
	t.lifetime.mu.Unlock()

	if q == 0 && f == 0 && ts == 0 {
		return
	}

	ctx := context.Background()
	const upsert = `INSERT INTO sense_metrics (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = value + excluded.value`
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("sense metrics: flush begin tx: %v", err)
		return
	}
	if _, err := tx.ExecContext(ctx, upsert, "lifetime_queries", q); err != nil {
		log.Printf("sense metrics: flush queries: %v", err)
	}
	if _, err := tx.ExecContext(ctx, upsert, "lifetime_file_reads_avoided", f); err != nil {
		log.Printf("sense metrics: flush file_reads_avoided: %v", err)
	}
	if _, err := tx.ExecContext(ctx, upsert, "lifetime_tokens_saved", ts); err != nil {
		log.Printf("sense metrics: flush tokens_saved: %v", err)
	}
	if err := tx.Commit(); err != nil {
		log.Printf("sense metrics: flush commit: %v", err)
	}
}

func (t *Tracker) loadPersisted(ctx context.Context) LifetimeCounters {
	var lc LifetimeCounters
	rows, err := t.db.QueryContext(ctx, `SELECT key, value FROM sense_metrics`)
	if err != nil {
		return lc
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var value int
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		switch key {
		case "lifetime_queries":
			lc.Queries = value
		case "lifetime_file_reads_avoided":
			lc.FileReadsAvoided = value
		case "lifetime_tokens_saved":
			lc.TokensSaved = value
		}
	}
	return lc
}

// SessionCounters holds in-memory session-scoped counters.
type SessionCounters struct {
	Queries          int
	FileReadsAvoided int
	TokensSaved      int
}

// LifetimeCounters holds all-time counters (persisted + pending).
type LifetimeCounters struct {
	Queries          int
	FileReadsAvoided int
	TokensSaved      int
}

// TopQueryInfo holds the single highest-saving query this session.
type TopQueryInfo struct {
	Tool        string
	Args        string
	TokensSaved int
}
