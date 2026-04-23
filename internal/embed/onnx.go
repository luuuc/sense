package embed

import (
	"context"
	"fmt"
	"math"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var ortOnce sync.Once
var ortInitErr error

// BatchSize is the number of symbols processed per ONNX inference call.
// Bounds memory usage; not a user-tuning knob.
const BatchSize = 50

// MaxSequenceLength is the maximum token sequence length the model accepts.
const MaxSequenceLength = 128

// ONNXEmbedder wraps an ONNX Runtime session for all-MiniLM-L6-v2 inference.
// Not safe for concurrent use.
type ONNXEmbedder struct {
	session   *ort.DynamicAdvancedSession
	tokenizer *tokenizer

	// Pre-allocated buffers reused across embedBatch calls.
	inputIDs      []int64
	attentionMask []int64
	tokenTypeIDs  []int64
}

// NewONNXEmbedder creates an embedder from model and vocabulary bytes.
// The caller must have previously called InitORTLibrary.
// intraOpThreads controls per-session thread parallelism; use 0 for the
// ONNX Runtime default (all cores).
func NewONNXEmbedder(modelBytes []byte, vocabBytes []byte, intraOpThreads int) (*ONNXEmbedder, error) {
	inputNames := []string{"input_ids", "attention_mask", "token_type_ids"}
	outputNames := []string{"last_hidden_state"}

	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("create session options: %w", err)
	}
	defer func() { _ = opts.Destroy() }()
	if intraOpThreads > 0 {
		if err := opts.SetIntraOpNumThreads(intraOpThreads); err != nil {
			return nil, fmt.Errorf("set intra-op threads: %w", err)
		}
	}
	if err := opts.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll); err != nil {
		return nil, fmt.Errorf("set graph optimization level: %w", err)
	}

	session, err := ort.NewDynamicAdvancedSessionWithONNXData(
		modelBytes,
		inputNames,
		outputNames,
		opts,
	)
	if err != nil {
		return nil, fmt.Errorf("create ONNX session: %w", err)
	}

	tok := newTokenizer(vocabBytes, MaxSequenceLength)

	bufSize := int64(BatchSize) * int64(MaxSequenceLength)
	return &ONNXEmbedder{
		session:       session,
		tokenizer:     tok,
		inputIDs:      make([]int64, bufSize),
		attentionMask: make([]int64, bufSize),
		tokenTypeIDs:  make([]int64, bufSize),
	}, nil
}

// InitORTLibrary initializes the ONNX Runtime shared library. Must be
// called once before creating any ONNXEmbedder instances. The libPath
// is the path to the ONNX Runtime shared library (.dylib / .so).
// Safe to call multiple times; only the first call takes effect.
func InitORTLibrary(libPath string) error {
	ortOnce.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		ortInitErr = ort.InitializeEnvironment()
	})
	return ortInitErr
}

func (e *ONNXEmbedder) Embed(ctx context.Context, inputs []EmbedInput) ([][]float32, error) {
	results := make([][]float32, 0, len(inputs))

	for i := 0; i < len(inputs); i += BatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		end := i + BatchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		batch := inputs[i:end]

		vecs, err := e.embedBatch(batch)
		if err != nil {
			return nil, fmt.Errorf("embed batch starting at %d: %w", i, err)
		}
		results = append(results, vecs...)
	}
	return results, nil
}

func (e *ONNXEmbedder) embedBatch(batch []EmbedInput) ([][]float32, error) {
	batchSize := int64(len(batch))
	seqLen := int64(MaxSequenceLength)
	needed := batchSize * seqLen

	// Use pre-allocated buffers, sliced to actual batch size.
	inputIDs := e.inputIDs[:needed]
	attentionMask := e.attentionMask[:needed]
	tokenTypeIDs := e.tokenTypeIDs[:needed]

	clear(inputIDs)
	clear(attentionMask)
	clear(tokenTypeIDs)

	for i, in := range batch {
		text := FormatInput(in)
		tok := e.tokenizer.Tokenize(text)
		offset := int64(i) * seqLen
		for j := 0; j < int(seqLen); j++ {
			inputIDs[offset+int64(j)] = int64(tok.InputIDs[j])
			attentionMask[offset+int64(j)] = int64(tok.AttentionMask[j])
			tokenTypeIDs[offset+int64(j)] = int64(tok.TokenTypeIDs[j])
		}
	}

	shape := ort.Shape{batchSize, seqLen}

	inputIDsTensor, err := ort.NewTensor(shape, inputIDs)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer func() { _ = inputIDsTensor.Destroy() }()

	attentionMaskTensor, err := ort.NewTensor(shape, attentionMask)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer func() { _ = attentionMaskTensor.Destroy() }()

	tokenTypeIDsTensor, err := ort.NewTensor(shape, tokenTypeIDs)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer func() { _ = tokenTypeIDsTensor.Destroy() }()

	outputTensor, err := ort.NewEmptyTensor[float32](ort.Shape{batchSize, seqLen, int64(Dimensions)})
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}
	defer func() { _ = outputTensor.Destroy() }()

	err = e.session.Run(
		[]ort.Value{inputIDsTensor, attentionMaskTensor, tokenTypeIDsTensor},
		[]ort.Value{outputTensor},
	)
	if err != nil {
		return nil, fmt.Errorf("ONNX inference: %w", err)
	}

	// Mean pooling over the sequence dimension, masked by attention.
	output := outputTensor.GetData()
	results := make([][]float32, batchSize)

	for i := int64(0); i < batchSize; i++ {
		vec := make([]float32, Dimensions)
		var tokenCount float32

		for j := int64(0); j < seqLen; j++ {
			if attentionMask[i*seqLen+j] == 0 {
				continue
			}
			tokenCount++
			baseIdx := i*seqLen*int64(Dimensions) + j*int64(Dimensions)
			for k := 0; k < Dimensions; k++ {
				vec[k] += output[baseIdx+int64(k)]
			}
		}

		if tokenCount > 0 {
			for k := range vec {
				vec[k] /= tokenCount
			}
		}

		// L2 normalize
		normalize(vec)
		results[i] = vec
	}

	return results, nil
}

func (e *ONNXEmbedder) Close() error {
	if e.session != nil {
		return e.session.Destroy()
	}
	return nil
}

func normalize(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	norm := float32(math.Sqrt(sum))
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
}
