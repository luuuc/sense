package embed

import "testing"

// TestLookupID covers both branches of lookupID — a known token returns its id,
// an unknown token falls back to 0 — using an inline vocabulary so the test
// needs no downloaded model files.
func TestLookupID(t *testing.T) {
	vocab := []byte("[PAD]\n[UNK]\n[CLS]\n[SEP]\nhello\nworld\n")
	tok := newTokenizer(vocab, 16)

	// newTokenizer resolves the special tokens through lookupID; confirm the
	// hit path landed them on the right ids.
	if tok.clsID != 2 {
		t.Errorf("clsID = %d, want 2", tok.clsID)
	}
	if tok.sepID != 3 {
		t.Errorf("sepID = %d, want 3", tok.sepID)
	}

	if got := tok.lookupID("hello"); got != 4 {
		t.Errorf("lookupID(hello) = %d, want 4", got)
	}
	// Miss branch: a token absent from the vocab falls back to 0.
	if got := tok.lookupID("absent-token"); got != 0 {
		t.Errorf("lookupID(absent) = %d, want 0", got)
	}
}
