package mcpio

import "strconv"

// SourceText labels a SearchResultEntry that came from the substring
// text-fallback path rather than the keyword/vector retrieval legs. The
// other Source values (keyword/vector/hybrid/graph) originate in the
// search engine; "text" originates here, so it is named here.
const SourceText = "text"

// TextMatch holds the fields needed to convert a text fallback hit into
// a SearchResultEntry. Defined here (rather than importing search.TextResult)
// to keep mcpio free of upstream dependencies on the search package.
type TextMatch struct {
	File  string
	Line  int
	Match string
}

// ConvertTextResults builds SearchResultEntry values from text fallback
// matches, deduplicating against files already present in structural
// results. Returns the converted entries and whether any were added.
func ConvertTextResults(matches []TextMatch, structuralEntries []SearchResultEntry) ([]SearchResultEntry, bool) {
	seen := make(map[string]struct{}, len(structuralEntries))
	for _, e := range structuralEntries {
		key := e.File + ":" + strconv.Itoa(e.Line)
		seen[key] = struct{}{}
	}

	var entries []SearchResultEntry
	for _, m := range matches {
		key := m.File + ":" + strconv.Itoa(m.Line)
		if _, dup := seen[key]; dup {
			continue
		}
		entries = append(entries, SearchResultEntry{
			File:    m.File,
			Line:    m.Line,
			Kind:    "text_match",
			Snippet: m.Match,
			Source:  SourceText,
		})
	}
	return entries, len(entries) > 0
}
