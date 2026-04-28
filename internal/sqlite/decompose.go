package sqlite

import (
	"strings"
	"unicode"
)

var namespaceSplitter = strings.NewReplacer("::", " ", ".", " ", "/", " ")

// Decompose splits an identifier into space-separated lowercase tokens.
// It handles CamelCase (CopyPaymentLink → copy payment link),
// snake_case (handle_payment_error → handle payment error), and
// acronyms (HTTPSHandler → https handler). Namespace separators
// (., ::, /) are treated as word boundaries.
func Decompose(name string) string {
	name = namespaceSplitter.Replace(name)

	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == ' ' || r == '-'
	})

	var tokens []string
	for _, part := range parts {
		if part == "" {
			continue
		}
		split := splitCamelCase(part)
		for _, s := range split {
			if allUpper(s) && len(s) > 5 {
				tokens = append(tokens, splitAcronymRun(s)...)
			} else {
				tokens = append(tokens, s)
			}
		}
	}
	if len(tokens) == 0 {
		return strings.ToLower(name)
	}
	return strings.ToLower(strings.Join(tokens, " "))
}

// splitCamelCase splits a CamelCase identifier on case boundaries.
// Runs of uppercase are kept as single tokens (acronyms):
// HTTPSHandler → [HTTPS, Handler], getURL → [get, URL].
func splitCamelCase(s string) []string {
	runes := []rune(s)
	if len(runes) <= 1 {
		return []string{s}
	}
	var result []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev := unicode.IsUpper(runes[i-1])
		curr := unicode.IsUpper(runes[i])

		if !prev && curr {
			result = append(result, string(runes[start:i]))
			start = i
		} else if prev && !curr && i-start > 1 {
			result = append(result, string(runes[start:i-1]))
			start = i - 1
		}
	}
	result = append(result, string(runes[start:]))
	return result
}

var knownAcronyms = map[string]bool{
	"ID": true, "IO": true, "IP": true, "OS": true, "UI": true, "DB": true,
	"API": true, "CLI": true, "CSS": true, "CSV": true, "CDN": true,
	"DNS": true, "EOF": true, "FTP": true, "GUI": true, "JWT": true,
	"RPC": true, "SDK": true, "SQL": true, "SSH": true, "SSL": true,
	"TCP": true, "TLS": true, "UDP": true, "URI": true, "URL": true,
	"XML": true, "AWS": true, "GCP": true,
	"GRPC": true, "HTML": true, "HTTP": true, "JSON": true,
	"REST": true, "SMTP": true, "TOML": true, "UUID": true, "YAML": true,
	"HTTPS": true,
}

// splitAcronymRun splits a fully-uppercase string into known acronyms.
// Greedy from left, longest match first. Unknown remainders stay as one token.
func splitAcronymRun(s string) []string {
	var result []string
	for len(s) > 0 {
		matched := false
		maxLen := len(s)
		if maxLen > 5 {
			maxLen = 5
		}
		for l := maxLen; l >= 2; l-- {
			if knownAcronyms[s[:l]] {
				result = append(result, s[:l])
				s = s[l:]
				matched = true
				break
			}
		}
		if !matched {
			result = append(result, s)
			break
		}
	}
	return result
}

func allUpper(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}
