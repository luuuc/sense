#!/usr/bin/env bash
# Downloads model assets and ONNX Runtime libraries for all build targets.
# Run before `goreleaser` or `make build` to populate internal/embed/bundle/.
#
# Usage:
#   ./scripts/fetch-deps.sh           # all platforms
#   ./scripts/fetch-deps.sh --local   # current platform only (dev builds)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$SCRIPT_DIR/.."
BUNDLE="$ROOT/internal/embed/bundle"

ORT_VERSION="1.24.4"
MODEL_REPO="sentence-transformers/all-MiniLM-L6-v2"

mkdir -p "$BUNDLE"

# ── Model + Vocabulary (platform-independent) ────────────────────────
# Single quantized model for all platforms. ONNX format is platform-neutral;
# the runtime adapts execution to the host ISA. Using one model ensures
# identical embedding vectors regardless of where the binary runs.

if [ ! -f "$BUNDLE/model.onnx" ]; then
    MODEL_URL="https://huggingface.co/$MODEL_REPO/resolve/main/onnx/model_qint8_arm64.onnx"
    echo "Downloading model..."
    curl -fSL "$MODEL_URL" -o "$BUNDLE/model.onnx"
fi

if [ ! -f "$BUNDLE/vocab.txt" ]; then
    echo "Downloading vocabulary..."
    curl -fSL "https://huggingface.co/$MODEL_REPO/resolve/main/vocab.txt" -o "$BUNDLE/vocab.txt"
fi

# ── ONNX Runtime shared libraries ───────────────────────────────────

fetch_ort() {
    local os="$1" arch="$2" target_dir="$3"
    local libname url archive_dir

    mkdir -p "$target_dir"

    case "$os" in
        darwin)
            libname="libonnxruntime.dylib"
            if [ "$arch" = "arm64" ]; then
                url="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-osx-arm64-${ORT_VERSION}.tgz"
                archive_dir="onnxruntime-osx-arm64-${ORT_VERSION}"
            else
                url="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-osx-x86_64-${ORT_VERSION}.tgz"
                archive_dir="onnxruntime-osx-x86_64-${ORT_VERSION}"
            fi
            ;;
        linux)
            libname="libonnxruntime.so"
            if [ "$arch" = "arm64" ]; then
                url="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-aarch64-${ORT_VERSION}.tgz"
                archive_dir="onnxruntime-linux-aarch64-${ORT_VERSION}"
            else
                url="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-${ORT_VERSION}.tgz"
                archive_dir="onnxruntime-linux-x64-${ORT_VERSION}"
            fi
            ;;
    esac

    if [ -f "$target_dir/$libname" ]; then
        return
    fi

    echo "Downloading ONNX Runtime for ${os}/${arch}..."
    local tmp
    tmp=$(mktemp -d)
    curl -fSL "$url" -o "$tmp/ort.tgz"
    tar -xzf "$tmp/ort.tgz" -C "$tmp"
    cp "$tmp/$archive_dir/lib/$libname" "$target_dir/$libname"
    rm -rf "$tmp"
}

if [ "${1:-}" = "--local" ]; then
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    [ "$ARCH" = "x86_64" ] && ARCH="amd64"
    [ "$ARCH" = "aarch64" ] && ARCH="arm64"
    fetch_ort "$OS" "$ARCH" "$BUNDLE/${OS}_${ARCH}"
else
    fetch_ort darwin arm64 "$BUNDLE/darwin_arm64"
    fetch_ort darwin amd64 "$BUNDLE/darwin_amd64"
    fetch_ort linux amd64 "$BUNDLE/linux_amd64"
    fetch_ort linux arm64 "$BUNDLE/linux_arm64"
fi

echo "Dependencies ready in $BUNDLE"
ls -lh "$BUNDLE/model.onnx"
