// Package embed provides semantic embedding generation for Sense symbols.
// It wraps ONNX inference with a batched interface suitable for the scan
// pipeline's pass-3 embedding step.
package embed

import "context"

// EmbedInput carries the data needed to produce one embedding vector.
// Context (from ContextForFile) is the primary content; Snippet holds
// the code body. For query embeddings, only Snippet is set.
type EmbedInput struct {
	QualifiedName string
	Kind          string
	Snippet       string
	Context       string
}

// ModelID identifies the bundled embedding model and format version.
// Stored in sense_meta at embed time; compared on index open to detect
// model or format changes that require re-embedding.
const ModelID = "all-MiniLM-L6-v2-ctx1"

// Dimensions is the embedding vector size produced by all-MiniLM-L6-v2.
const Dimensions = 384

// Embedder generates dense vector embeddings from symbol metadata.
// Implementations are not required to be safe for concurrent use.
type Embedder interface {
	Embed(ctx context.Context, inputs []EmbedInput) ([][]float32, error)
	Close() error
}

// FormatContext produces the text fed to the embedding model. When
// graph-derived Context is available, it forms the primary content
// with the code Snippet appended. When Context is absent but
// structural fields exist (Kind, QualifiedName), they provide a
// minimal identity header. For queries (no Context, no Kind), the
// raw Snippet is returned as-is.
func FormatContext(in EmbedInput) string {
	if in.Context != "" {
		if in.Snippet != "" {
			return in.Context + "\n" + in.Snippet
		}
		return in.Context
	}
	if in.Kind != "" {
		s := in.Kind + " " + in.QualifiedName
		if in.Snippet != "" {
			s += "\n" + in.Snippet
		}
		return s
	}
	return in.Snippet
}
