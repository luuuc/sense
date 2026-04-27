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

# ── Version coupling ────────────────────────────────────────────────
# The Go bindings (github.com/yalue/onnxruntime_go) compile against a
# specific ORT C API version (ORT_API_VERSION in onnxruntime_c_api.h).
# The shared library's GetApi() returns NULL if asked for a version it
# doesn't support, so the library major version must match the bindings:
#
#   onnxruntime_go v1.27.0 → ORT_API_VERSION 24 → needs ORT ≥ 1.24.x
#   onnxruntime_go v1.28.0 → ORT_API_VERSION 25 → needs ORT ≥ 1.25.x
#
# ORT dropped macOS x86_64 builds after v1.23.1, so darwin/amd64 is
# pinned to the last release that ships an x86_64 dylib. Bumping the
# Go bindings past v1.27.0 (API 24) would break Mac Intel entirely
# because no ORT ≥ 1.24 ships an x86_64 build (API 23 < 24 still works
# since the library supports all prior API versions).
#
# TL;DR: if you bump onnxruntime_go, bump ORT_VERSION to match and
# verify ORT_VERSION_DARWIN_AMD64 still satisfies the new API version.
ORT_VERSION="1.24.4"
ORT_VERSION_DARWIN_AMD64="1.23.1"  # last ORT release with macOS x86_64 builds
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

    # ORT 1.24+ dropped macOS x86_64 builds; use 1.23.0 for darwin/amd64
    local ver="$ORT_VERSION"
    if [ "$os" = "darwin" ] && [ "$arch" = "amd64" ]; then
        ver="$ORT_VERSION_DARWIN_AMD64"
    fi

    mkdir -p "$target_dir"

    case "$os" in
        darwin)
            libname="libonnxruntime.dylib"
            if [ "$arch" = "arm64" ]; then
                url="https://github.com/microsoft/onnxruntime/releases/download/v${ver}/onnxruntime-osx-arm64-${ver}.tgz"
                archive_dir="onnxruntime-osx-arm64-${ver}"
            else
                url="https://github.com/microsoft/onnxruntime/releases/download/v${ver}/onnxruntime-osx-x86_64-${ver}.tgz"
                archive_dir="onnxruntime-osx-x86_64-${ver}"
            fi
            ;;
        linux)
            libname="libonnxruntime.so"
            if [ "$arch" = "arm64" ]; then
                url="https://github.com/microsoft/onnxruntime/releases/download/v${ver}/onnxruntime-linux-aarch64-${ver}.tgz"
                archive_dir="onnxruntime-linux-aarch64-${ver}"
            else
                url="https://github.com/microsoft/onnxruntime/releases/download/v${ver}/onnxruntime-linux-x64-${ver}.tgz"
                archive_dir="onnxruntime-linux-x64-${ver}"
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
