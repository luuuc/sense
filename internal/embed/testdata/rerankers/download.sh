#!/usr/bin/env bash
# Downloads cross-encoder ONNX models for reranker evaluation.
# Run from the repo root:
#   bash internal/embed/testdata/rerankers/download.sh
#
# Requires: curl
# Models share vocab.txt with the embedding model (same BERT-base-uncased vocabulary).
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"

download_model() {
    local name="$1"
    local hf_repo="$2"
    local dest="$DIR/$name"

    if [ -f "$dest/model.onnx" ]; then
        echo "$name: already downloaded ($(du -h "$dest/model.onnx" | cut -f1))"
        return
    fi

    mkdir -p "$dest"

    # Try quantized (INT8 ARM64) first, then unquantized.
    local urls=(
        "https://huggingface.co/$hf_repo/resolve/main/onnx/model_qint8_arm64.onnx"
        "https://huggingface.co/$hf_repo/resolve/main/onnx/model_quantized.onnx"
        "https://huggingface.co/$hf_repo/resolve/main/onnx/model.onnx"
    )

    for url in "${urls[@]}"; do
        echo "$name: trying $url ..."
        if curl -fSL "$url" -o "$dest/model.onnx" 2>/dev/null; then
            echo "$name: downloaded ($(du -h "$dest/model.onnx" | cut -f1))"
            return
        fi
    done

    echo "$name: all URLs failed. Export manually:"
    echo "  pip install optimum[exporters] onnxruntime"
    echo "  optimum-cli export onnx --model $hf_repo $dest/"
    rm -rf "$dest"
    return 1
}

# Candidate 1: ms-marco-MiniLM-L-6-v2 (6 layers, ~22MB quantized / ~80MB FP32)
download_model "ms-marco-MiniLM-L-6-v2" "cross-encoder/ms-marco-MiniLM-L-6-v2"

# Candidate 2: ms-marco-MiniLM-L-12-v2 (12 layers, ~33MB quantized / ~130MB FP32)
download_model "ms-marco-MiniLM-L-12-v2" "cross-encoder/ms-marco-MiniLM-L-12-v2"

echo ""
echo "Model sizes:"
du -h "$DIR"/*/model.onnx 2>/dev/null || echo "(no models downloaded)"
