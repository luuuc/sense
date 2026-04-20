package embed

import (
	"strings"
	"unicode"
)

// tokenizer implements BERT-style WordPiece tokenization for
// all-MiniLM-L6-v2. It handles lowercasing, punctuation splitting,
// and subword decomposition using the model's vocabulary.
type tokenizer struct {
	vocab   map[string]int32
	maxLen  int
	clsID   int32
	sepID   int32
	unkID   int32
	padID   int32
}

func newTokenizer(vocabData []byte, maxLen int) *tokenizer {
	vocab := make(map[string]int32)
	lines := strings.Split(string(vocabData), "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			vocab[line] = int32(i)
		}
	}

	t := &tokenizer{
		vocab:  vocab,
		maxLen: maxLen,
	}
	t.clsID = t.lookupID("[CLS]")
	t.sepID = t.lookupID("[SEP]")
	t.unkID = t.lookupID("[UNK]")
	t.padID = t.lookupID("[PAD]")
	return t
}

func (t *tokenizer) lookupID(token string) int32 {
	if id, ok := t.vocab[token]; ok {
		return id
	}
	return 0
}

// tokenizeResult holds the three tensors BERT models expect.
type tokenizeResult struct {
	InputIDs      []int32
	AttentionMask []int32
	TokenTypeIDs  []int32
}

// Tokenize converts text to model inputs. Truncates to maxLen,
// pads to maxLen.
func (t *tokenizer) Tokenize(text string) tokenizeResult {
	tokens := t.wordpiece(text)

	// Truncate to maxLen - 2 (leaving room for [CLS] and [SEP])
	maxTokens := t.maxLen - 2
	if len(tokens) > maxTokens {
		tokens = tokens[:maxTokens]
	}

	ids := make([]int32, t.maxLen)
	mask := make([]int32, t.maxLen)
	typeIDs := make([]int32, t.maxLen)

	ids[0] = t.clsID
	mask[0] = 1
	for i, tok := range tokens {
		ids[i+1] = tok
		mask[i+1] = 1
	}
	ids[len(tokens)+1] = t.sepID
	mask[len(tokens)+1] = 1

	return tokenizeResult{
		InputIDs:      ids,
		AttentionMask: mask,
		TokenTypeIDs:  typeIDs,
	}
}

// wordpiece runs basic tokenization (lowercase, split on whitespace +
// punctuation) followed by WordPiece subword splitting.
func (t *tokenizer) wordpiece(text string) []int32 {
	text = strings.ToLower(text)

	// Basic tokenization: split on whitespace and punctuation
	words := basicTokenize(text)

	var ids []int32
	for _, word := range words {
		subIDs := t.wordpieceSingle(word)
		ids = append(ids, subIDs...)
	}
	return ids
}

// wordpieceSingle splits a single word into WordPiece subwords.
func (t *tokenizer) wordpieceSingle(word string) []int32 {
	if _, ok := t.vocab[word]; ok {
		return []int32{t.vocab[word]}
	}

	var ids []int32
	start := 0
	for start < len(word) {
		end := len(word)
		var foundID int32
		found := false
		for end > start {
			substr := word[start:end]
			if start > 0 {
				substr = "##" + substr
			}
			if id, ok := t.vocab[substr]; ok {
				foundID = id
				found = true
				break
			}
			end--
		}
		if !found {
			ids = append(ids, t.unkID)
			break
		}
		ids = append(ids, foundID)
		start = end
	}
	return ids
}

// basicTokenize splits text on whitespace and punctuation, treating
// each punctuation character as its own token.
func basicTokenize(text string) []string {
	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsSpace(r) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		if isPunct(r) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(r))
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func isPunct(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}
