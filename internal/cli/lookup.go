package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Match is one candidate returned by Lookup — the minimum a CLI needs
// to render a disambiguation list and continue with a resolved id.
type Match struct {
	ID        int64
	Name      string
	Qualified string
	Kind      string
	File      string
	Language  string
	LineStart int
}

// Lookup resolves a user-supplied symbol string into matching rows
// in the sense_symbols table. The search runs three tiers in order
// and returns the first tier that produces any match:
//
//  1. Exact qualified name (`qualified = ?`)
//  2. Exact unqualified name (`name = ?`)
//  3. Fuzzy (Levenshtein ≤ 2 against both `qualified` and `name`),
//     requires query length ≥ 3, returns at most fuzzyMaxResults
//
// Tier 3 is only consulted when both exact tiers come up empty — a
// user who typed a valid qualified name that exists gets that row,
// not a fuzzy alternative. The returned slice is ordered by
// qualified-name ascending so the CLI's disambiguation output is
// alphabetical and stable across runs.
//
// Caller contract:
//   - len(matches) == 0 → not-found; render "no symbol" message, exit 2
//   - len(matches) == 1 → resolved; use matches[0].ID
//   - len(matches) > 1  → ambiguous; render disambiguation, exit 2
func Lookup(ctx context.Context, db *sql.DB, query string) ([]Match, error) {
	if query == "" {
		return nil, nil
	}

	matches, err := lookupByQualified(ctx, db, query)
	if err != nil {
		return nil, err
	}
	if len(matches) > 0 {
		return matches, nil
	}

	matches, err = lookupByName(ctx, db, query)
	if err != nil {
		return nil, err
	}
	if len(matches) > 0 {
		return matches, nil
	}

	return lookupFuzzy(ctx, db, query)
}

// fuzzyMinQueryLen is the smallest query we bother fuzz-matching. A
// two-character query like "Us" sits within distance 2 of thousands
// of symbols in a real repo; below three characters, fuzzy produces
// more confusion than help.
const fuzzyMinQueryLen = 3

// fuzzyMaxDistance caps Levenshtein distance. Two edits catches
// typical single-letter typos and one-char transpositions on
// identifiers of any length — for longer edits users are better
// served by asking with more context.
const fuzzyMaxDistance = 2

// fuzzyMaxResults caps the number of candidates returned from the
// fuzzy tier. Five is enough to scan with eyes; more becomes a
// paragraph and defeats the purpose of a hint.
const fuzzyMaxResults = 5

// lookupByQualified resolves tier 1 — an exact `qualified` match.
func lookupByQualified(ctx context.Context, db *sql.DB, value string) ([]Match, error) {
	const q = `SELECT s.id, s.name, s.qualified, s.kind, f.path, f.language, s.line_start
	           FROM sense_symbols s
	           JOIN sense_files   f ON f.id = s.file_id
	           WHERE s.qualified = ?
	           ORDER BY s.qualified ASC`
	return scanMatches(ctx, db, q, value)
}

// lookupByName resolves tier 2 — an exact `name` (unqualified)
// match. When a repo contains multiple namespaces' worth of the
// same short name (`App::User`, `Admin::User`), this is the tier
// that typically produces an ambiguity list.
func lookupByName(ctx context.Context, db *sql.DB, value string) ([]Match, error) {
	const q = `SELECT s.id, s.name, s.qualified, s.kind, f.path, f.language, s.line_start
	           FROM sense_symbols s
	           JOIN sense_files   f ON f.id = s.file_id
	           WHERE s.name = ?
	           ORDER BY s.qualified ASC`
	return scanMatches(ctx, db, q, value)
}

// lookupFuzzy streams every symbol's (id, qualified, name, file,
// line) and keeps the ones within fuzzyMaxDistance on either column.
// At pitch scale (≤30K symbols) this is a few tens of milliseconds;
// if that becomes a bottleneck, a future card could trigram-index
// the columns. Not this card's problem.
func lookupFuzzy(ctx context.Context, db *sql.DB, query string) ([]Match, error) {
	if len(query) < fuzzyMinQueryLen {
		return nil, nil
	}
	const q = `SELECT s.id, s.name, s.qualified, s.kind, f.path, f.language, s.line_start
	           FROM sense_symbols s
	           JOIN sense_files   f ON f.id = s.file_id`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("lookup fuzzy: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type scored struct {
		match    Match
		distance int
	}
	var hits []scored
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.ID, &m.Name, &m.Qualified, &m.Kind, &m.File, &m.Language, &m.LineStart); err != nil {
			return nil, fmt.Errorf("lookup fuzzy scan: %w", err)
		}
		dq := levenshtein(query, m.Qualified)
		dn := levenshtein(query, m.Name)
		d := dq
		if dn < d {
			d = dn
		}
		if d <= fuzzyMaxDistance {
			hits = append(hits, scored{match: m, distance: d})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lookup fuzzy iterate: %w", err)
	}

	// Closer matches first; ties broken alphabetically so output is
	// deterministic regardless of SQLite's row order.
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].distance != hits[j].distance {
			return hits[i].distance < hits[j].distance
		}
		return hits[i].match.Qualified < hits[j].match.Qualified
	})
	if len(hits) > fuzzyMaxResults {
		hits = hits[:fuzzyMaxResults]
	}
	out := make([]Match, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.match)
	}
	return out, nil
}

// scanMatches is the shared row-scan for the two exact-tier queries.
// The SELECT column order in callers must match Match field order
// below.
func scanMatches(ctx context.Context, db *sql.DB, q string, args ...any) ([]Match, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("lookup: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.ID, &m.Name, &m.Qualified, &m.Kind, &m.File, &m.Language, &m.LineStart); err != nil {
			return nil, fmt.Errorf("lookup scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// FilterMatches narrows a match list by file path substring and/or
// language. Returns the original slice unmodified when both filters
// are empty.
func filterMatches(matches []Match, file, language string) []Match {
	if file == "" && language == "" {
		return matches
	}
	var out []Match
	for _, m := range matches {
		if file != "" && !strings.Contains(m.File, file) {
			continue
		}
		if language != "" && !strings.EqualFold(m.Language, language) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// PrintDisambiguation writes the "multiple candidates" list the
// pitch's example shows: a numbered list of candidates with kind +
// file:line, followed by a hint to narrow with --file or --language.
// Written to stderr by the CLI so a user piping --json still sees
// the hint on their terminal.
func PrintDisambiguation(w io.Writer, query, commandHint string, matches []Match) {
	_, _ = fmt.Fprintf(w, "Multiple symbols match %q:\n", query)
	for i, m := range matches {
		_, _ = fmt.Fprintf(w, "  %d. %s  (%s, %s)  %s:%d\n", i+1, m.Qualified, m.Kind, m.Language, m.File, m.LineStart)
	}
	if commandHint != "" {
		_, _ = fmt.Fprintf(w, "Narrow with: %s %q --file <path> or --language <lang>\n", commandHint, query)
	}
}

// PrintNotFound writes the not-found message. When fuzzy returned
// candidates (they got promoted into matches because neither exact
// tier produced any), the caller still sees them as a disambiguation
// list — fuzzy distinguishes itself semantically by arriving via
// the fallback tier, not by a different render.
func PrintNotFound(w io.Writer, query string) {
	_, _ = fmt.Fprintf(w, "No symbol matches %q. Run 'sense scan' if the index is stale.\n", query)
}

// ---------------------------------------------------------------
// Levenshtein — classic O(len(a)*len(b)) DP. The graph CLI's query
// sizes (human-typed identifiers, typically <40 chars) keep this
// cheap even when run against 30K symbols.
// ---------------------------------------------------------------

// levenshtein returns the edit distance between a and b, lower-cased
// for case-insensitive matching (a Ruby user typing `checkoutservice`
// should still find `CheckoutService`). Early-outs on trivially
// equal / empty inputs; otherwise a two-row rolling buffer computes
// the DP table.
func levenshtein(a, b string) int {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
