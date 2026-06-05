package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/conventions"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/sqlite"
)

// keySymbolsLimit caps the key symbols included in a conventions response.
const keySymbolsLimit = 12

func (h *handlers) handleConventions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	domain := req.GetString("domain", "")
	minStrength := req.GetFloat("min_strength", h.defaults.ConventionsMinStrength)

	results, symbolCount, err := conventions.Detect(ctx, h.db, conventions.Options{
		Domain:      domain,
		MinStrength: minStrength,
	})
	if err != nil {
		return nil, fmt.Errorf("sense_conventions: %w", err)
	}

	keyEntries, err := buildKeyEntries(ctx, h.adapter, domain, keySymbolsLimit)
	if err != nil {
		return nil, fmt.Errorf("sense_conventions: key symbols: %w", err)
	}

	instanceCap := h.defaults.ConventionsInstanceCap
	filesAvoided := min(symbolCount/5, 30)
	resp := mcpio.ConventionsResponse{
		KeySymbols: keyEntries,
		SenseMetrics: mcpio.ConventionsMetrics{
			SymbolsAnalyzed:           symbolCount,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * mcpio.AvgTokensPerFile,
		},
	}
	for _, c := range results {
		// Display labels disambiguate same-named representatives; snippet lookup
		// keys on the raw symbol names (sense_symbols.name), so it must use the
		// bare PickRepresentatives output, not the labels.
		labels := conventions.RepresentativeLabels(c.Examples, instanceCap)
		rawNames := conventions.PickRepresentatives(c.Examples, instanceCap)
		snippets := lookupInstanceSnippets(ctx, h.db, rawNames, 3)
		resp.Conventions = append(resp.Conventions, mcpio.ConventionEntry{
			Category:       string(c.Category),
			Description:    c.Description,
			Strength:       mcpio.Confidence(c.Strength),
			Instances:      labels,
			TotalInstances: c.Instances,
			KeySymbol:      c.KeySymbol,
			Snippets:       snippets,
		})
	}

	mcpio.ApplyTokenBudget(&resp, h.defaults.ConventionsTokenBudget)
	mcpio.BuildConventionsSummary(&resp)

	h.tracker.Record("sense_conventions", domain,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved, false)

	resp.NextSteps = conventionsHints(resp, domain)

	out, err := mcpio.MarshalConventionsCompact(resp)
	if err != nil {
		return nil, fmt.Errorf("sense_conventions: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func conventionsHints(resp mcpio.ConventionsResponse, domain string) []mcpio.NextStep {
	var hints []mcpio.NextStep

	for _, c := range resp.Conventions {
		if float64(c.Strength) >= 0.8 {
			hints = append(hints, mcpio.NextStep{
				Tool:   "sense_search",
				Args:   map[string]any{"query": c.Description},
				Reason: "strong convention — search for all instances",
			})
			break
		}
	}

	if domain != "" && len(hints) < mcpio.MaxNextSteps {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_conventions",
			Reason: "scoped results — run without domain filter for project-wide patterns",
		})
	}

	if len(hints) > mcpio.MaxNextSteps {
		hints = hints[:mcpio.MaxNextSteps]
	}
	return hints
}

func buildKeyEntries(ctx context.Context, adapter *sqlite.Adapter, domain string, limit int) ([]mcpio.KeySymbolEntry, error) {
	keySymbols, err := adapter.TopSymbolsByReach(ctx, domain, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]mcpio.KeySymbolEntry, 0, len(keySymbols))
	for _, ks := range keySymbols {
		callers, _ := adapter.TopCallers(ctx, ks.ID, 3)
		callerNames := make([]string, len(callers))
		for i, c := range callers {
			callerNames[i] = c.Qualified
		}
		entries = append(entries, mcpio.KeySymbolEntry{
			Name:       ks.Qualified,
			Kind:       ks.Kind,
			Snippet:    ks.Snippet,
			References: ks.RefFiles,
			Callers:    callerNames,
		})
	}
	return entries, nil
}

func lookupInstanceSnippets(ctx context.Context, db *sql.DB, instances []string, limit int) []string {
	if len(instances) == 0 {
		return nil
	}
	n := min(len(instances), limit)
	names := instances[:n]
	placeholders := strings.Repeat("?,", len(names))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT snippet FROM sense_symbols WHERE name IN (` + placeholders + `) AND snippet IS NOT NULL AND snippet != '' LIMIT ?`
	args := make([]any, len(names)+1)
	for i, name := range names {
		args[i] = name
	}
	args[len(names)] = limit
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil && s != "" {
			out = append(out, s)
		}
	}
	return out
}
