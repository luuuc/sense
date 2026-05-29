package mcpio

import "testing"

func TestConvertTextResultsBasic(t *testing.T) {
	matches := []TextMatch{
		{File: "schema.sql", Line: 1, Match: "CREATE TABLE users"},
		{File: "main.go", Line: 10, Match: "func main()"},
	}
	entries, fired := ConvertTextResults(matches, nil)
	if !fired {
		t.Fatal("expected fired=true")
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Source != "text" {
		t.Errorf("Source = %q, want %q", entries[0].Source, "text")
	}
	if entries[0].Kind != "text_match" {
		t.Errorf("Kind = %q, want %q", entries[0].Kind, "text_match")
	}
}

func TestConvertTextResultsDeduplicates(t *testing.T) {
	existing := []SearchResultEntry{
		{File: "schema.sql", Line: 1, Kind: "type", Source: "keyword"},
	}
	matches := []TextMatch{
		{File: "schema.sql", Line: 1, Match: "CREATE TABLE users"},
		{File: "config.go", Line: 5, Match: "MAX_RETRIES"},
	}
	entries, fired := ConvertTextResults(matches, existing)
	if !fired {
		t.Fatal("expected fired=true (config.go should pass)")
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (schema.sql:1 should be deduped)", len(entries))
	}
	if entries[0].File != "config.go" {
		t.Errorf("File = %q, want %q", entries[0].File, "config.go")
	}
}

func TestConvertTextResultsAllDuplicates(t *testing.T) {
	existing := []SearchResultEntry{
		{File: "a.go", Line: 10, Source: "keyword"},
	}
	matches := []TextMatch{
		{File: "a.go", Line: 10, Match: "something"},
	}
	entries, fired := ConvertTextResults(matches, existing)
	if fired {
		t.Error("expected fired=false when all results are duplicates")
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestConvertTextResultsEmpty(t *testing.T) {
	entries, fired := ConvertTextResults(nil, nil)
	if fired {
		t.Error("expected fired=false for nil input")
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestConvertTextResultsSameFileDifferentLine(t *testing.T) {
	existing := []SearchResultEntry{
		{File: "schema.sql", Line: 1, Source: "keyword"},
	}
	matches := []TextMatch{
		{File: "schema.sql", Line: 5, Match: "ALTER TABLE"},
	}
	entries, fired := ConvertTextResults(matches, existing)
	if !fired {
		t.Fatal("expected fired=true (different line should not be deduped)")
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
}
