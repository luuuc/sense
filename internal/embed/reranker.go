package embed

import (
	"fmt"

	ort "github.com/yalue/onnxruntime_go"
)

// RerankerBatchSize is the max pairs per ONNX inference call.
const RerankerBatchSize = 50

// ONNXReranker implements Reranker using a cross-encoder ONNX model.
// Not safe for concurrent use.
type ONNXReranker struct {
	session   *ort.DynamicAdvancedSession
	tokenizer *tokenizer
	seqLen    int

	inputIDs      []int64
	attentionMask []int64
	tokenTypeIDs  []int64
}

// NewONNXReranker creates a reranker from cross-encoder model and
// vocabulary bytes. The model must accept input_ids, attention_mask,
// and token_type_ids, and output logits of shape [batch, 1].
func NewONNXReranker(modelBytes, vocabBytes []byte, intraOpThreads int) (*ONNXReranker, error) {
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
		return nil, fmt.Errorf("set graph optimization: %w", err)
	}

	session, err := ort.NewDynamicAdvancedSessionWithONNXData(
		modelBytes,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"logits"},
		opts,
	)
	if err != nil {
		return nil, fmt.Errorf("create cross-encoder session: %w", err)
	}

	tok := newTokenizer(vocabBytes, MaxSequenceLength)

	bufSize := int64(RerankerBatchSize) * int64(MaxSequenceLength)
	return &ONNXReranker{
		session:       session,
		tokenizer:     tok,
		seqLen:        MaxSequenceLength,
		inputIDs:      make([]int64, bufSize),
		attentionMask: make([]int64, bufSize),
		tokenTypeIDs:  make([]int64, bufSize),
	}, nil
}

// Score returns a relevance score for each (query, doc) pair.
// Higher scores indicate greater relevance. Handles batching
// internally when len(docs) > RerankerBatchSize.
func (r *ONNXReranker) Score(query string, docs []string) ([]float32, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	scores := make([]float32, 0, len(docs))
	for i := 0; i < len(docs); i += RerankerBatchSize {
		end := i + RerankerBatchSize
		if end > len(docs) {
			end = len(docs)
		}

		batchScores, err := r.scoreBatch(query, docs[i:end])
		if err != nil {
			return nil, fmt.Errorf("score batch at %d: %w", i, err)
		}
		scores = append(scores, batchScores...)
	}

	return scores, nil
}

func (r *ONNXReranker) scoreBatch(query string, docs []string) ([]float32, error) {
	batchSize := int64(len(docs))
	seqLen := int64(r.seqLen)
	needed := batchSize * seqLen

	inputIDs := r.inputIDs[:needed]
	attentionMask := r.attentionMask[:needed]
	tokenTypeIDs := r.tokenTypeIDs[:needed]

	clear(inputIDs)
	clear(attentionMask)
	clear(tokenTypeIDs)

	for i, doc := range docs {
		pair := r.tokenizer.TokenizePair(query, doc)
		offset := int64(i) * seqLen
		for j := int64(0); j < seqLen; j++ {
			inputIDs[offset+j] = int64(pair.InputIDs[j])
			attentionMask[offset+j] = int64(pair.AttentionMask[j])
			tokenTypeIDs[offset+j] = int64(pair.TokenTypeIDs[j])
		}
	}

	shape := ort.Shape{batchSize, seqLen}

	idT, err := ort.NewTensor(shape, inputIDs)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer func() { _ = idT.Destroy() }()

	maskT, err := ort.NewTensor(shape, attentionMask)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer func() { _ = maskT.Destroy() }()

	typeT, err := ort.NewTensor(shape, tokenTypeIDs)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer func() { _ = typeT.Destroy() }()

	outT, err := ort.NewEmptyTensor[float32](ort.Shape{batchSize, 1})
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}
	defer func() { _ = outT.Destroy() }()

	if err := r.session.Run([]ort.Value{idT, maskT, typeT}, []ort.Value{outT}); err != nil {
		return nil, fmt.Errorf("cross-encoder inference: %w", err)
	}

	data := outT.GetData()
	scores := make([]float32, batchSize)
	copy(scores, data)
	return scores, nil
}

func (r *ONNXReranker) Close() error {
	if r.session != nil {
		err := r.session.Destroy()
		r.session = nil
		return err
	}
	return nil
}
