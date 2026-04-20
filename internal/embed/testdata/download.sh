#!/usr/bin/env bash
# Downloads the all-MiniLM-L6-v2 quantized ONNX model and vocabulary
# for development and testing. Run from the repo root:
#   bash internal/embed/testdata/download.sh
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"

MODEL_URL="https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/onnx/model_qint8_arm64.onnx"
VOCAB_URL="https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/vocab.txt"

if [ ! -f "$DIR/model.onnx" ]; then
    echo "Downloading quantized all-MiniLM-L6-v2 model..."
    curl -fSL "$MODEL_URL" -o "$DIR/model.onnx"
else
    echo "Model already downloaded."
fi

if [ ! -f "$DIR/vocab.txt" ]; then
    echo "Downloading vocabulary..."
    curl -fSL "$VOCAB_URL" -o "$DIR/vocab.txt"
else
    echo "Vocabulary already downloaded."
fi

echo "Done. Model: $(du -h "$DIR/model.onnx" | cut -f1), Vocab: $(wc -l < "$DIR/vocab.txt") tokens"
