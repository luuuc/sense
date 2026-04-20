// Package embed provides semantic embedding generation for Sense symbols.
// It wraps ONNX inference with a batched interface suitable for the scan
// pipeline's pass-3 embedding step.
package embed

import "context"

// EmbedInput is the concatenation defined in the data model: qualified
// name + kind + parent name + code snippet (first 512 tokens).
type EmbedInput struct {
	QualifiedName string
	Kind          string
	ParentName    string
	Snippet       string
}

// ModelID identifies the bundled embedding model. Stored in sense_meta
// at embed time; compared on index open to detect model changes.
const ModelID = "all-MiniLM-L6-v2"

// Dimensions is the embedding vector size produced by all-MiniLM-L6-v2.
const Dimensions = 384

// Embedder generates dense vector embeddings from symbol metadata.
// Implementations are not required to be safe for concurrent use.
type Embedder interface {
	Embed(ctx context.Context, inputs []EmbedInput) ([][]float32, error)
	Close() error
}

// FormatInput produces the text representation of an EmbedInput that
// gets tokenized and fed to the model. The format is:
//
//	<kind> <qualified_name> [parent: <parent>] <snippet>
func FormatInput(in EmbedInput) string {
	s := in.Kind + " " + in.QualifiedName
	if in.ParentName != "" {
		s += " parent: " + in.ParentName
	}
	if in.Snippet != "" {
		s += " " + in.Snippet
	}
	return s
}
