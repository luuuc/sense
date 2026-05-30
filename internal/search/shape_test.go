package search

import "testing"

// TestClassifyQuery pins the lexical classifier against a labeled table
// spanning all three shapes, with at least one adversarial case each:
//   - a long snake_case identifier must classify as Identifier, NOT NL,
//     even though splitting it yields four "words";
//   - a 2-word plain phrase must classify as Mixed, NOT NL;
//   - an NL sentence with an embedded identifier must classify as Mixed,
//     NOT NaturalLanguage.
//
// The headline query the pitch exists to fix must be NaturalLanguage.
func TestClassifyQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  QueryShape
	}{
		// --- Identifier ---
		{"single plain word", "checkout", ShapeIdentifier},
		{"single camelCase token", "HandleHTTPRequest", ShapeIdentifier},
		{"adversarial: long snake_case", "cannot_add_own_listing", ShapeIdentifier},
		{"dotted + snake single token", "User.find_by_email", ShapeIdentifier},
		{"two PascalCase tokens", "UserController OrderService", ShapeIdentifier},
		{"two snake_case tokens", "find_by_email cannot_offer_on_own_listing", ShapeIdentifier},

		// --- NaturalLanguage ---
		{"headline query", "prevent users from buying their own items", ShapeNaturalLanguage},
		{"question form", "how does authentication work?", ShapeNaturalLanguage},
		{"long sentence with stopwords", "where is the user validated before checkout", ShapeNaturalLanguage},
		{"four words with stopwords", "buying their own items", ShapeNaturalLanguage},
		{"four plain words, no stopword (token-count clause)", "create user account record", ShapeNaturalLanguage},

		// --- Mixed ---
		{"adversarial: 2-word plain phrase", "prevent purchase", ShapeMixed},
		{"three plain words no stopword", "create user account", ShapeMixed},
		{"leading-capital is not an identifier", "validate User input", ShapeMixed},
		{"adversarial: NL sentence with embedded identifier", "where is UserController defined", ShapeMixed},
		{"plain words plus snake_case token", "parse the config_file", ShapeMixed},

		// --- degenerate ---
		{"empty", "", ShapeIdentifier},
		{"whitespace only", "   ", ShapeIdentifier},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyQuery(tt.query)
			if got != tt.want {
				t.Errorf("classifyQuery(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

// TestClassifyQueryPureLexical documents and checks the pitch's hard
// requirement: classification touches neither the embedder nor the
// database. The structural guarantee is the signature — classifyQuery is
// a free function taking only a string, with no Engine receiver, adapter,
// or embedder in scope. The behavioral guarantee checked here is that it
// is a pure deterministic function of its input: repeated calls on a fresh
// process with no Engine constructed return identical results.
func TestClassifyQueryPureLexical(t *testing.T) {
	const q = "prevent users from buying their own items"
	first := classifyQuery(q)
	for i := 0; i < 100; i++ {
		if got := classifyQuery(q); got != first {
			t.Fatalf("classifyQuery not deterministic: call %d = %v, first = %v", i, got, first)
		}
	}
	// No Engine, adapter, or embedder was constructed in this test; the
	// call above produced a result regardless, proving the classifier is
	// self-contained.
	if first != ShapeNaturalLanguage {
		t.Fatalf("headline query classified as %v, want NaturalLanguage", first)
	}
}

// TestResolveShape pins the mode escape hatch at the shape level: an
// explicit mode overrides the classifier, while hybrid/empty/unknown defer
// to it. The identifier-shaped query proves the override is real — semantic
// mode forces NaturalLanguage on a query the classifier would call
// Identifier, and keyword mode forces Identifier on an NL query.
func TestResolveShape(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		query string
		want  QueryShape
	}{
		{"semantic forces NL on an identifier", ModeSemantic, "cannot_add_own_listing", ShapeNaturalLanguage},
		{"keyword forces Identifier on an NL query", ModeKeyword, "prevent users from buying their own items", ShapeIdentifier},
		{"hybrid defers to classifier (NL)", ModeHybrid, "prevent users from buying their own items", ShapeNaturalLanguage},
		{"empty mode defers to classifier (identifier)", "", "cannot_add_own_listing", ShapeIdentifier},
		{"unknown mode defers to classifier", "bogus", "prevent users from buying their own items", ShapeNaturalLanguage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveShape(tt.mode, tt.query); got != tt.want {
				t.Errorf("resolveShape(%q, %q) = %v, want %v", tt.mode, tt.query, got, tt.want)
			}
		})
	}
}

func TestQueryShapeString(t *testing.T) {
	tests := []struct {
		shape QueryShape
		want  string
	}{
		{ShapeIdentifier, "identifier"},
		{ShapeNaturalLanguage, "natural_language"},
		{ShapeMixed, "mixed"},
		{QueryShape(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.shape.String(); got != tt.want {
			t.Errorf("QueryShape(%d).String() = %q, want %q", tt.shape, got, tt.want)
		}
	}
}

// TestIsIdentifierShaped pins the token-level identifier detector,
// especially the boundary the classifier relies on: a leading-capital word
// is NOT identifier-shaped (it is indistinguishable from a sentence-initial
// capital), while an internal case change or internal separator is.
func TestIsIdentifierShaped(t *testing.T) {
	tests := []struct {
		tok  string
		want bool
	}{
		{"snake_case", true},
		{"kebab_not", true}, // underscore present
		{"camelCase", true},
		{"PascalCase", true},
		{"HTTPServer", true}, // acronym → word transition
		{"User.save", true},  // internal dot
		{"Foo::Bar", true},   // internal colon
		{"path/to", true},    // internal slash
		{"User", false},      // leading capital only
		{"Prevent", false},   // sentence-initial capital
		{"listing", false},   // plain lowercase
		{"items.", false},    // trailing period is not internal
		{".hidden", false},   // leading dot is not internal
		{"a", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isIdentifierShaped(tt.tok); got != tt.want {
			t.Errorf("isIdentifierShaped(%q) = %v, want %v", tt.tok, got, tt.want)
		}
	}
}
