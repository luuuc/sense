// Package blast computes a symbol's blast radius: the set of symbols
// that would be affected (directly or indirectly) if the subject
// changed. The traversal is a reverse-direction BFS on `calls` edges
// — the subject's callers, their callers, and so on up to MaxHops.
//
// The engine reads through a plain *sql.DB handle so it can be used
// by any SQLite consumer (CLI in 01-04, MCP server in 01-05, or
// future watch-mode tooling) without coupling to the sqlite.Adapter
// write path.
//
// Cards 11 (risk formula) and 12 (test association) fill their parts
// of the Result — risk classification and the AffectedTests list.
// This card exposes the Result shape and wires the BFS so those
// follow-ons are additive.
package blast

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/luuuc/sense/internal/model"
)

// defaultMaxHops matches the pitch's acceptance-criterion call:
// Options{MaxHops: 3, IncludeTests: true}. Callers that pass
// MaxHops: 0 get three hops of traversal — deep enough to be useful
// for blast-radius questions, shallow enough to stay fast on a 30K
// symbol graph.
const defaultMaxHops = 3

// Options bounds a blast computation. The zero-value Options{} is a
// valid "give me sensible defaults" request:
//
//   - MaxHops 0 (unset) ⇒ three hops, matching the pitch's acceptance
//     criterion. A caller that explicitly wants zero traversal (the
//     subject alone) cannot express it with this field; the blast
//     question "who calls me, at any distance" is the API's purpose,
//     so treating zero as "none" would be a surprising way to spend
//     the zero value.
//   - MinConfidence 0 ⇒ accept every edge regardless of confidence.
//   - IncludeTests false ⇒ AffectedTests stays empty; callers opt in.
type Options struct {
	MaxHops       int
	MinConfidence float64
	IncludeTests  bool
}

// CallerHop describes one indirect caller — a symbol reachable from
// the subject via more than one calls-edge step. Via is the caller
// one hop closer to the subject (predecessor on the BFS shortest
// path), so a consumer can render "X calls Y which calls <subject>".
// Hops is 1-indexed from the subject: Hops=2 means "callers of
// direct callers".
type CallerHop struct {
	Symbol model.Symbol
	Via    model.Symbol
	Hops   int
}

// Result is the full blast-radius answer for one subject symbol. The
// shape is fixed by the pitch's public API.
//
// Risk is one of RiskLow / RiskMedium / RiskHigh. RiskReasons is
// guaranteed to have at least one entry so consumers can read
// `Reasons[0]` for the primary factor without a length check; the
// slice type leaves room for additional factors (e.g. "crosses
// module boundary") when the pitch's extension clause triggers.
// AffectedTests is populated only when Options.IncludeTests is set;
// the empty, non-nil default keeps JSON encoders stable.
type Result struct {
	Symbol          model.Symbol
	Risk            string
	RiskReasons     []string
	DirectCallers   []model.Symbol
	IndirectCallers []CallerHop
	AffectedTests   []string
	TotalAffected   int
}

// Compute returns the blast radius of symbolID under the given
// options. Errors surface only for I/O failures or a missing subject
// symbol; an absence of callers is a successful empty result, not an
// error.
//
// The pitch's pseudocode signature omitted context.Context because
// it was sketched as an API shape, not a type. Real callers (the CLI
// in 01-04 and the MCP server in 01-05) need cancellation — a blast
// on a wide graph can run tens of milliseconds, and an MCP server
// answering concurrent requests should never hold an uncancellable
// query. ctx flows through every I/O hop below.
func Compute(ctx context.Context, db *sql.DB, symbolID int64, opts Options) (Result, error) {
	if opts.MaxHops <= 0 {
		opts.MaxHops = defaultMaxHops
	}

	subject, err := loadSymbol(ctx, db, symbolID)
	if err != nil {
		return Result{}, fmt.Errorf("blast: load subject %d: %w", symbolID, err)
	}

	// BFS bookkeeping. visited maps caller id → hop count. predecessor
	// maps caller id → the id it called in the previous frontier
	// (enables Via reconstruction for indirect hops). Both are keyed
	// on symbol id; a symbol appears at most once at its shortest-
	// path hop distance.
	visited := map[int64]int{subject.ID: 0}
	predecessor := map[int64]int64{}
	frontier := []int64{subject.ID}

	for hop := 1; hop <= opts.MaxHops; hop++ {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("blast: cancelled at hop %d: %w", hop, err)
		}
		pairs, err := expandFrontier(ctx, db, frontier, opts.MinConfidence)
		if err != nil {
			return Result{}, fmt.Errorf("blast: hop %d: %w", hop, err)
		}
		var next []int64
		for _, pair := range pairs {
			if _, seen := visited[pair.source]; seen {
				continue
			}
			visited[pair.source] = hop
			predecessor[pair.source] = pair.target
			next = append(next, pair.source)
		}
		if len(next) == 0 {
			break
		}
		frontier = next
	}

	// Split visited into direct (hop=1) and indirect (hop>1) callers.
	// Then hydrate both sets to model.Symbol in a single bulk read.
	var directIDs, indirectIDs []int64
	for id, hops := range visited {
		switch hops {
		case 0:
			continue // the subject itself
		case 1:
			directIDs = append(directIDs, id)
		default:
			indirectIDs = append(indirectIDs, id)
		}
	}

	allIDs := append([]int64{}, directIDs...)
	allIDs = append(allIDs, indirectIDs...)
	// Also hydrate predecessors so CallerHop.Via can reference them.
	// Predecessors are either the subject or another caller already
	// in allIDs; adding them defensively keeps the lookup map whole
	// without a second query.
	predIDs := map[int64]struct{}{}
	for _, predID := range predecessor {
		predIDs[predID] = struct{}{}
	}
	for id := range predIDs {
		if id == subject.ID {
			continue
		}
		if _, seen := visited[id]; seen {
			continue
		}
		allIDs = append(allIDs, id)
	}

	symbolsByID, err := loadSymbols(ctx, db, allIDs)
	if err != nil {
		return Result{}, fmt.Errorf("blast: hydrate callers: %w", err)
	}
	symbolsByID[subject.ID] = subject

	directCallers := make([]model.Symbol, 0, len(directIDs))
	for _, id := range directIDs {
		if sym, ok := symbolsByID[id]; ok {
			directCallers = append(directCallers, sym)
		}
	}
	sortSymbolsByID(directCallers)

	indirectCallers := make([]CallerHop, 0, len(indirectIDs))
	for _, id := range indirectIDs {
		sym, ok := symbolsByID[id]
		if !ok {
			continue
		}
		via := symbolsByID[predecessor[id]]
		indirectCallers = append(indirectCallers, CallerHop{
			Symbol: sym,
			Via:    via,
			Hops:   visited[id],
		})
	}
	sortHopsByID(indirectCallers)

	affectedTests := []string{}
	if opts.IncludeTests {
		testIDs := append([]int64{subject.ID}, directIDs...)
		testIDs = append(testIDs, indirectIDs...)
		tests, err := loadTestsTargeting(ctx, db, testIDs)
		if err != nil {
			return Result{}, fmt.Errorf("blast: load tests: %w", err)
		}
		affectedTests = tests
	}

	// Card 11 replaces the placeholder risk. Filling the zero value
	// now keeps the struct shape valid so callers written against the
	// public API compile today.
	risk, reasons := classifyRisk(len(directCallers))

	return Result{
		Symbol:          subject,
		Risk:            risk,
		RiskReasons:     reasons,
		DirectCallers:   directCallers,
		IndirectCallers: indirectCallers,
		AffectedTests:   affectedTests,
		TotalAffected:   len(directCallers) + len(indirectCallers),
	}, nil
}

// edgePair is the (source, target) shape one BFS hop returns. source
// is the caller we're learning about; target is the node we already
// visited and are expanding from.
type edgePair struct {
	source int64
	target int64
}

// expandFrontier runs the BFS hop query: "which symbols reference
// anything in frontier via calls, composes, includes, or inherits
// edges at or above MinConfidence?" Returns (source_id, target_id)
// pairs so the outer loop can track predecessors for Via
// reconstruction.
//
// Large frontiers are chunked to stay under SQLite's default
// SQLITE_MAX_VARIABLE_NUMBER (999) — at pitch scale (~30K symbols)
// frontiers are typically small, but the chunking guard keeps the
// function robust if a hot subject produces an unusually wide hop.
func expandFrontier(ctx context.Context, db *sql.DB, frontier []int64, minConfidence float64) ([]edgePair, error) {
	const chunk = 500
	var out []edgePair
	for start := 0; start < len(frontier); start += chunk {
		end := start + chunk
		if end > len(frontier) {
			end = len(frontier)
		}
		batch := frontier[start:end]

		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]

		q := `SELECT source_id, target_id FROM sense_edges
		      WHERE target_id IN (` + placeholders + `)
		        AND source_id IS NOT NULL
		        AND kind IN ('calls', 'composes', 'includes', 'inherits')
		        AND confidence >= ?`

		args := make([]any, 0, len(batch)+1)
		for _, id := range batch {
			args = append(args, id)
		}
		args = append(args, minConfidence)

		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p edgePair
			if err := rows.Scan(&p.source, &p.target); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out = append(out, p)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// Risk tier labels. Exported so CLI / MCP consumers can compare
// against named values instead of string literals. The pitch fixes
// the three tiers as the complete classifier vocabulary; adding a
// fourth tier would require re-shaping the formula, not just adding
// a constant.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// Risk tier thresholds. Kept as named constants so a reader scanning
// classifyRisk sees the pitch numbers without context — "10" alone
// is arbitrary; "riskHighThreshold" is policy. The pitch explicitly
// says to extend this formula only when a real case proves three
// tiers insufficient, so the thresholds are fixed until then.
const (
	riskHighThreshold   = 10
	riskMediumThreshold = 3
)

// classifyRisk implements the pitch's three-tier formula:
//
//	direct_callers >= 10  → high
//	direct_callers >= 3   → medium
//	otherwise             → low
//
// RiskReasons carries one human-readable line per factor. Today the
// only factor is the direct-caller count; the pitch's "crosses
// module boundary" example is reserved for when a real case argues
// for it, so the slice is always a single element. Consumers that
// want to check risk programmatically should branch on the Risk
// string (or the Risk* constants) rather than parsing Reasons.
func classifyRisk(directCallers int) (string, []string) {
	reasons := []string{directCallersReason(directCallers)}
	switch {
	case directCallers >= riskHighThreshold:
		return RiskHigh, reasons
	case directCallers >= riskMediumThreshold:
		return RiskMedium, reasons
	default:
		return RiskLow, reasons
	}
}

// directCallersReason formats the direct-caller count as a human
// sentence — "1 direct caller" vs "12 direct callers" — so the
// slice reads naturally in CLI or MCP output.
func directCallersReason(n int) string {
	if n == 1 {
		return "1 direct caller"
	}
	return fmt.Sprintf("%d direct callers", n)
}

// loadTestsTargeting returns the file paths of test files whose
// `tests` edges target any of the given symbol ids. Card 12's
// test-association extractor populates those edges; until then, this
// function naturally returns an empty slice because no tests edges
// exist yet.
func loadTestsTargeting(ctx context.Context, db *sql.DB, ids []int64) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT DISTINCT f.path
	      FROM sense_edges e
	      LEFT JOIN sense_symbols s ON s.id = e.source_id
	      JOIN sense_files f ON f.id = COALESCE(s.file_id, e.file_id)
	      WHERE e.target_id IN (` + placeholders + `)
	        AND e.kind = 'tests'
	      ORDER BY f.path`
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []string{}
	}
	return out, rows.Err()
}
