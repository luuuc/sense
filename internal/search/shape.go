package search

import (
	"strings"
	"unicode"
)

// QueryShape is the lexical shape of a search query. It steers fusion
// weighting (see shapeWeights): an identifier and a natural-language
// sentence must not be scored by the identical keyword/vector régime.
type QueryShape int

const (
	// ShapeIdentifier is a query that looks like code: a single token, or
	// multiple tokens all of identifier form (snake_case, CamelCase,
	// dotted/namespaced). The keyword leg is trustworthy here.
	ShapeIdentifier QueryShape = iota
	// ShapeNaturalLanguage is a sentence-like query: multiple plain words,
	// stopwords, or a question. The keyword leg tends to latch onto
	// high-frequency generic tokens, so the vector leg must be trusted.
	ShapeNaturalLanguage
	// ShapeMixed is a short phrase or a sentence with an embedded
	// identifier — neither leg should dominate.
	ShapeMixed
)

// String renders a QueryShape for logs and metadata.
func (s QueryShape) String() string {
	switch s {
	case ShapeIdentifier:
		return "identifier"
	case ShapeNaturalLanguage:
		return "natural_language"
	case ShapeMixed:
		return "mixed"
	default:
		return "unknown"
	}
}

// resolveShape determines the query shape to steer fusion with, honoring
// an explicit request-mode override. ModeSemantic and ModeKeyword let a
// caller who knows the query's nature bypass the classifier; ModeHybrid
// (or an empty/unrecognized mode) runs the automatic classifier.
func resolveShape(mode, query string) QueryShape {
	switch mode {
	case ModeSemantic:
		return ShapeNaturalLanguage
	case ModeKeyword:
		return ShapeIdentifier
	default:
		return classifyQuery(query)
	}
}

// naturalLanguageMinTokens is the token count at or above which a query of
// plain words with no identifier tokens is treated as natural language
// even without stopwords. Below it (a 2–3 word phrase) the query is Mixed.
const naturalLanguageMinTokens = 4

// classifyQuery determines a query's lexical shape using only the query
// string — no embedding call, no database access. Signals: token count,
// presence of identifier-shaped tokens, stopword/plain-word counts, and a
// trailing question mark.
//
// Rules, in order:
//   - empty           → Identifier (degenerate; preserves keyword-heavy default)
//   - single token    → Identifier (no sentence structure to trust the vector leg)
//   - identifier tokens AND natural-language words → Mixed
//   - identifier tokens only                       → Identifier
//   - no identifier tokens, but a question / stopword / ≥4 tokens → NaturalLanguage
//   - otherwise (a 2–3 word plain phrase)          → Mixed
func classifyQuery(q string) QueryShape {
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		return ShapeIdentifier
	}
	if len(tokens) == 1 {
		return ShapeIdentifier
	}

	hasQuestion := strings.Contains(q, "?")
	var ident, natural int
	for _, tok := range tokens {
		if isIdentifierShaped(tok) {
			ident++
		} else {
			// Stopwords and plain words alike count as natural-language
			// words for the purpose of detecting sentence structure.
			natural++
		}
	}

	switch {
	case ident > 0 && natural > 0:
		return ShapeMixed
	case ident > 0:
		return ShapeIdentifier
	case hasQuestion || hasStopword(tokens) || len(tokens) >= naturalLanguageMinTokens:
		return ShapeNaturalLanguage
	default:
		return ShapeMixed
	}
}

// isIdentifierShaped reports whether a token carries structural markers of
// a code identifier: an internal separator (_ :: / .) or a camelCase /
// PascalCase internal case change. A leading-capital word ("User",
// "Prevent") is NOT identifier-shaped on its own — it is indistinguishable
// from a sentence-initial capital — so only an *internal* case boundary
// counts.
func isIdentifierShaped(tok string) bool {
	if strings.Contains(tok, "_") {
		return true
	}
	if strings.Contains(tok, "::") {
		return true
	}
	if hasInternalSeparator(tok, '/') || hasInternalSeparator(tok, '.') {
		return true
	}
	return hasInternalCaseChange(tok)
}

// hasInternalSeparator reports whether sep appears between two
// alphanumeric runes (so a trailing sentence period or a leading slash
// does not count). "User.save" and "Foo::Bar" qualify; "items." does not.
func hasInternalSeparator(tok string, sep rune) bool {
	runes := []rune(tok)
	for i, r := range runes {
		if r != sep {
			continue
		}
		if i > 0 && i < len(runes)-1 &&
			isAlphaNum(runes[i-1]) && isAlphaNum(runes[i+1]) {
			return true
		}
	}
	return false
}

// hasInternalCaseChange reports a camelCase/PascalCase boundary: a
// lowercase letter immediately followed by an uppercase one ("myVar",
// "HandleRequest"), or an acronym→word transition ("HTTPServer"). A
// uniformly-capitalized first word is excluded by requiring the boundary
// to be internal.
func hasInternalCaseChange(tok string) bool {
	runes := []rune(tok)
	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) && unicode.IsLower(runes[i-1]) {
			return true
		}
		if i+1 < len(runes) &&
			unicode.IsUpper(runes[i-1]) && unicode.IsUpper(runes[i]) && unicode.IsLower(runes[i+1]) {
			return true
		}
	}
	return false
}

func isAlphaNum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// naturalLanguageVectorFloor is the minimum vector weight for a
// NaturalLanguage query. all-MiniLM-L6-v2 produces low absolute cosine
// similarities for NL→code even on correct matches, so confidence-based
// down-weighting would penalize the vector leg precisely when it is the
// only leg that can answer the query. Flooring decouples "how sure is the
// vector leg in absolute cosine terms" from "how much should we trust it
// relative to keyword for THIS query shape."
const naturalLanguageVectorFloor = 0.5

// shapeWeights resolves keyword/vector fusion weights from the query shape
// and the vector leg's confidence. It wraps fusionWeights (the confidence
// ladder) rather than renumbering it:
//   - Identifier      → fusionWeights unchanged (today's behavior, byte-for-byte).
//   - NaturalLanguage → fusionWeights, then the vector weight is floored at
//     naturalLanguageVectorFloor and keyword takes the remainder.
//   - Mixed           → balanced 0.5/0.5, so neither leg dominates.
func shapeWeights(shape QueryShape, vecConfidence float64) (keyword, vector float64) {
	kw, vec := fusionWeights(vecConfidence)
	switch shape {
	case ShapeNaturalLanguage:
		if vec < naturalLanguageVectorFloor {
			vec = naturalLanguageVectorFloor
			kw = 1.0 - vec
		}
		return kw, vec
	case ShapeMixed:
		return 0.5, 0.5
	default:
		return kw, vec
	}
}

// hasStopword reports whether any token (after trimming surrounding
// punctuation) is an English function word. A stopword signals sentence
// structure, which is the strongest cheap indicator of a natural-language
// query. This set is for *shape detection only* — it is deliberately NOT
// the mechanism for the generic-token ranking penalty, which is derived
// from corpus document frequency.
func hasStopword(tokens []string) bool {
	for _, tok := range tokens {
		word := strings.Trim(strings.ToLower(tok), ".,;:?!()\"'`")
		if _, ok := stopwords[word]; ok {
			return true
		}
	}
	return false
}

// stopwords are common English function words used as a sentence-structure
// signal by classifyQuery. Not a ranking mechanism — see hasStopword.
var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "this": {}, "that": {}, "these": {},
	"those": {}, "is": {}, "are": {}, "was": {}, "were": {}, "be": {},
	"been": {}, "being": {}, "am": {}, "do": {}, "does": {}, "did": {},
	"of": {}, "in": {}, "on": {}, "at": {}, "to": {}, "for": {},
	"from": {}, "by": {}, "with": {}, "without": {}, "into": {}, "onto": {},
	"about": {}, "as": {}, "and": {}, "or": {}, "but": {}, "not": {},
	"no": {}, "where": {}, "when": {}, "how": {}, "why": {}, "what": {},
	"which": {}, "who": {}, "whom": {}, "whose": {}, "their": {}, "them": {},
	"they": {}, "our": {}, "your": {}, "my": {}, "his": {}, "her": {},
	"its": {}, "it": {}, "we": {}, "you": {}, "i": {}, "he": {},
	"she": {}, "own": {}, "can": {}, "cannot": {}, "could": {},
	"should": {}, "would": {}, "will": {}, "shall": {}, "may": {},
	"might": {}, "must": {}, "all": {}, "any": {}, "some": {}, "if": {},
	"then": {}, "else": {}, "than": {}, "so": {}, "such": {}, "up": {},
	"out": {}, "over": {}, "under": {}, "there": {},
}
