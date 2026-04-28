package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Resolution describes which tier of the lookup chain produced a Match.
type Resolution string

const (
	ResExactQualified Resolution = "exact_qualified"
	ResExactName      Resolution = "exact_name"
	ResSuffix         Resolution = "suffix"
	ResContainment    Resolution = "containment"
	ResFuzzy          Resolution = "fuzzy"
)

// Match is one candidate returned by Lookup — the minimum a CLI needs
// to render a disambiguation list and continue with a resolved id.
type Match struct {
	ID         int64
	Name       string
	Qualified  string
	Kind       string
	File       string
	Language   string
	LineStart  int
	Resolution Resolution
}

// Lookup resolves a user-supplied symbol string into matching rows
// in the sense_symbols table. The search runs five tiers in order
// and returns the first tier that produces any match:
//
//  1. Exact qualified name (`qualified = ?`)
//  2. Exact unqualified name (`name = ?`)
//  3. Suffix match (`qualified LIKE '%' || ?`) — query is a suffix
//     of the qualified name, e.g. `TopicCreator#create` matches
//     `Discourse::TopicCreator#create`
//  4. Containment match (`name/qualified LIKE '%' || ? || '%'`) —
//     query appears anywhere in name or qualified
//  5. Fuzzy (Levenshtein ≤ 2 against name, qualified, and
//     separator-delimited suffixes of qualified), requires query
//     length ≥ 3, returns at most fuzzyMaxResults
//
// Each match carries a Resolution field indicating which tier
// produced it. Later tiers are only consulted when all earlier tiers
// come up empty. Suffix and containment tiers require query length
// ≥ 3 (likeMinQueryLen).
//
// Caller contract:
//   - len(matches) == 0 → not-found; render "no symbol" message
//   - len(matches) == 1 → resolved; use matches[0].ID
//   - len(matches) > 1  → ambiguous; render disambiguation
//   - Resolution == ResFuzzy → suggestions, not resolved matches
func Lookup(ctx context.Context, db *sql.DB, query string) ([]Match, error) {
	if query == "" {
		return nil, nil
	}

	matches, err := lookupByQualified(ctx, db, query)
	if err != nil {
		return nil, err
	}
	if len(matches) > 0 {
		return setResolution(matches, ResExactQualified), nil
	}

	matches, err = lookupByName(ctx, db, query)
	if err != nil {
		return nil, err
	}
	if len(matches) > 0 {
		return setResolution(matches, ResExactName), nil
	}

	matches, err = lookupBySuffix(ctx, db, query)
	if err != nil {
		return nil, err
	}
	if len(matches) > 0 {
		return setResolution(matches, ResSuffix), nil
	}

	matches, err = lookupByContainment(ctx, db, query)
	if err != nil {
		return nil, err
	}
	if len(matches) > 0 {
		return setResolution(matches, ResContainment), nil
	}

	matches, err = lookupFuzzy(ctx, db, query)
	if err != nil {
		return nil, err
	}
	return setResolution(matches, ResFuzzy), nil
}

func setResolution(matches []Match, r Resolution) []Match {
	for i := range matches {
		matches[i].Resolution = r
	}
	return matches
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

// likeMinQueryLen guards suffix and containment tiers against
// extremely short queries that would match most symbols.
const likeMinQueryLen = 3

// lookupBySuffix resolves tier 3 — the query is a suffix of the
// qualified name. Catches `TopicCreator#create` when the index has
// `Discourse::TopicCreator#create`.
func lookupBySuffix(ctx context.Context, db *sql.DB, value string) ([]Match, error) {
	if len(value) < likeMinQueryLen {
		return nil, nil
	}
	escaped := escapeLike(value)
	const q = `SELECT s.id, s.name, s.qualified, s.kind, f.path, f.language, s.line_start
	           FROM sense_symbols s
	           JOIN sense_files   f ON f.id = s.file_id
	           WHERE s.qualified LIKE '%' || ? ESCAPE '\'
	           ORDER BY s.qualified ASC`
	return scanMatches(ctx, db, q, escaped)
}

// lookupByContainment resolves tier 4 — the query appears somewhere
// in either the name or qualified column. Loosest match; may return
// many results.
func lookupByContainment(ctx context.Context, db *sql.DB, value string) ([]Match, error) {
	if len(value) < likeMinQueryLen {
		return nil, nil
	}
	escaped := escapeLike(value)
	const q = `SELECT s.id, s.name, s.qualified, s.kind, f.path, f.language, s.line_start
	           FROM sense_symbols s
	           JOIN sense_files   f ON f.id = s.file_id
	           WHERE s.name LIKE '%' || ? || '%' ESCAPE '\'
	              OR s.qualified LIKE '%' || ? || '%' ESCAPE '\'
	           ORDER BY s.qualified ASC`
	return scanMatches(ctx, db, q, escaped, escaped)
}

// escapeLike escapes the LIKE special characters %, _, and \ in user
// input so they are treated as literals. The ESCAPE '\' clause in the
// queries makes \ the escape character.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// lookupFuzzy streams every symbol and keeps the closest matches by
// Levenshtein distance. In addition to comparing against name and
// qualified columns, it compares against every separator-delimited
// suffix of the qualified name — so `TopicCreater#create` (typo)
// finds `Discourse::TopicCreator#create` via the suffix
// `TopicCreator#create` (distance 1).
//
// Results are capped at fuzzyMaxResults, sorted by distance then
// alphabetically. Only matches within fuzzyMaxDistance are returned.
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
		d := bestLevenshtein(query, m.Name, m.Qualified)
		if d <= fuzzyMaxDistance {
			hits = append(hits, scored{match: m, distance: d})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lookup fuzzy iterate: %w", err)
	}

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

// bestLevenshtein returns the minimum edit distance between query and
// a symbol's name, qualified name, and every separator-delimited
// suffix of the qualified name (`::#.`). This catches partial
// qualification typos: `TopicCreater#create` is distance 1 from the
// suffix `TopicCreator#create` of `Discourse::TopicCreator#create`.
func bestLevenshtein(query, name, qualified string) int {
	d := levenshtein(query, name)
	if d == 0 {
		return 0
	}
	if dq := levenshtein(query, qualified); dq < d {
		d = dq
		if d == 0 {
			return 0
		}
	}
	seen := map[string]struct{}{name: {}, qualified: {}}
	for _, sep := range []string{"::", "#", "."} {
		idx := 0
		for {
			pos := strings.Index(qualified[idx:], sep)
			if pos < 0 {
				break
			}
			idx += pos + len(sep)
			suffix := qualified[idx:]
			if _, dup := seen[suffix]; dup {
				continue
			}
			seen[suffix] = struct{}{}
			if ds := levenshtein(query, suffix); ds < d {
				d = ds
				if d == 0 {
					return 0
				}
			}
		}
	}
	return d
}

// EdgeCounts returns the total number of edges (source + target)
// for each symbol ID. Used to rank disambiguation candidates by
// connectedness.
func EdgeCounts(ctx context.Context, db *sql.DB, ids []int64) (map[int64]int, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)*2)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	ph := strings.Join(placeholders, ",")
	q := `SELECT symbol_id, SUM(cnt) FROM (
	          SELECT source_id AS symbol_id, COUNT(*) AS cnt
	          FROM sense_edges WHERE source_id IN (` + ph + `) GROUP BY source_id
	          UNION ALL
	          SELECT target_id AS symbol_id, COUNT(*) AS cnt
	          FROM sense_edges WHERE target_id IN (` + ph + `) GROUP BY target_id
	      ) GROUP BY symbol_id`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("edge counts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[int64]int, len(ids))
	for rows.Next() {
		var id int64
		var cnt int
		if err := rows.Scan(&id, &cnt); err != nil {
			return nil, fmt.Errorf("edge counts scan: %w", err)
		}
		counts[id] = cnt
	}
	return counts, rows.Err()
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
