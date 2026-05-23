package scan

import (
	"strings"
	"testing"
)

// TestCapDocstring_UnderCap returns the input unchanged.
func TestCapDocstring_UnderCap(t *testing.T) {
	in := "short docstring"
	got := capDocstring(in)
	if got != in {
		t.Errorf("capDocstring(%q) = %q, want unchanged", in, got)
	}
}

// TestCapDocstring_AtCap returns the input unchanged at the boundary.
func TestCapDocstring_AtCap(t *testing.T) {
	in := strings.Repeat("a", docstringMaxBytes)
	got := capDocstring(in)
	if got != in {
		t.Errorf("capDocstring at exact cap mutated the value (len %d → %d)", len(in), len(got))
	}
}

// TestCapDocstring_OverCap truncates to within the cap and appends the
// marker. The cap is the entire raison d'être of the helper; an input
// even one byte over the budget must come back inside it.
func TestCapDocstring_OverCap(t *testing.T) {
	in := strings.Repeat("a", docstringMaxBytes+1)
	got := capDocstring(in)
	if len(got) > docstringMaxBytes {
		t.Errorf("len(got) = %d, want ≤ %d", len(got), docstringMaxBytes)
	}
	if !strings.HasSuffix(got, docstringTruncMarker) {
		t.Errorf("missing truncation marker; tail = %q", got[max(0, len(got)-8):])
	}
}

// TestCapDocstring_RuneBoundary cuts on a rune boundary so the appended
// marker isn't glued to a half-multibyte sequence. Filling the buffer
// with three-byte runes (`日`) forces the truncation point to land
// inside a rune; the walk-back to the nearest rune start is what keeps
// the stored UTF-8 valid.
func TestCapDocstring_RuneBoundary(t *testing.T) {
	in := strings.Repeat("日", (docstringMaxBytes/3)+1) // > cap, all 3-byte runes
	got := capDocstring(in)
	if len(got) > docstringMaxBytes {
		t.Errorf("len(got) = %d, want ≤ %d", len(got), docstringMaxBytes)
	}
	// Strip the marker; the remaining prefix must be valid UTF-8 made
	// only of `日` runes.
	prefix := strings.TrimSuffix(got, docstringTruncMarker)
	if strings.ContainsRune(prefix, '�') {
		t.Errorf("prefix contains replacement rune; cut mid-rune")
	}
	if !strings.HasPrefix(in, prefix) {
		t.Errorf("prefix is not a prefix of input — cut shifted past rune start")
	}
}
