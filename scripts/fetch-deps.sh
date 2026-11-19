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
# Fetched from Microsoft's official NuGet package (Microsoft.ML.OnnxRuntime),
# not GitHub releases: github.com/microsoft/onnxruntime went 404 in Nov 2026.
# NuGet versions are immutable, and each pinned SHA-256 below was checked
# against the library shipped in Sense v1.16.3 (byte-identical for linux and
# darwin/amd64; darwin/arm64 is the same build, Microsoft-signed). A mismatch
# fails the build.

NUGET_DIR=$(mktemp -d)
trap 'rm -rf "$NUGET_DIR"' EXIT

sha256() {
    if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

fetch_ort() {
    local os="$1" arch="$2" target_dir="$3"
    local ver="$ORT_VERSION" libname rid want

    case "${os}_${arch}" in
        darwin_arm64) libname=libonnxruntime.dylib rid=osx-arm64
            want=0db33e160224e480c17932799d1dfaea59853b8e722b57bf61932c87e03586f0 ;;
        darwin_amd64) libname=libonnxruntime.dylib rid=osx-x64 ver="$ORT_VERSION_DARWIN_AMD64"
            want=583a6f3738eca06878c32cc1d14adac95af22689ad20d18933dbcc53974ced53 ;;
        linux_amd64) libname=libonnxruntime.so rid=linux-x64
            want=d132535d051344ff5c64c9c200004150559049a81ed330eb4422c1962fb6b7e4 ;;
        linux_arm64) libname=libonnxruntime.so rid=linux-arm64
            want=52b2a0e75e79468404284fec38ad5ee1a7a996232274f5e1b84f3e793fb07554 ;;
        *) echo "unsupported platform ${os}/${arch}" >&2; exit 1 ;;
    esac

    mkdir -p "$target_dir"
    if [ -f "$target_dir/$libname" ]; then
        return
    fi

    local nupkg="$NUGET_DIR/onnxruntime-${ver}.nupkg"
    if [ ! -f "$nupkg" ]; then
        echo "Downloading ONNX Runtime ${ver} (NuGet)..."
        curl -fSL "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/${ver}/microsoft.ml.onnxruntime.${ver}.nupkg" -o "$nupkg"
    fi

    echo "Extracting ONNX Runtime for ${os}/${arch}..."
    local tmp="$NUGET_DIR/$libname.${os}_${arch}"
    python3 -I -c 'import sys, zipfile; sys.stdout.buffer.write(zipfile.ZipFile(sys.argv[1]).read(sys.argv[2]))' \
        "$nupkg" "runtimes/${rid}/native/${libname}" > "$tmp"
    local got
    got=$(sha256 "$tmp")
    if [ "$got" != "$want" ]; then
        echo "checksum mismatch for ${os}/${arch}: got $got, want $want" >&2
        exit 1
    fi
    chmod 755 "$tmp"
    mv "$tmp" "$target_dir/$libname"
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
