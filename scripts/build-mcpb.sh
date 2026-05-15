#!/usr/bin/env bash
# Build a per-platform .mcpb bundle from a release tar.gz.
#
# Layout: <archive>.mcpb is a zip containing
#   manifest.json    (MCPB manifest v0.3, type=binary)
#   server/sense     (the binary, extracted from the tar.gz)
#
# Usage: build-mcpb.sh <archive.tar.gz> <version> <os> <arch> <outdir>

set -euo pipefail

if [ $# -ne 5 ]; then
  echo "usage: $0 <archive.tar.gz> <version> <os> <arch> <outdir>" >&2
  exit 2
fi

archive="$1"
version="$2"
os="$3"
arch="$4"
outdir="$5"

case "$os" in
  darwin) platform="darwin" ;;
  linux)  platform="linux" ;;
  *) echo "unsupported os: $os" >&2; exit 1 ;;
esac

script_dir="$(cd "$(dirname "$0")" && pwd)"
template="$script_dir/../mcpb/manifest.template.json"

if [ ! -f "$template" ]; then
  echo "manifest template not found: $template" >&2
  exit 1
fi

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

mkdir -p "$workdir/extract"
tar -xzf "$archive" -C "$workdir/extract"

binary="$(find "$workdir/extract" -type f -name sense | head -n1)"
if [ -z "$binary" ]; then
  echo "no 'sense' binary found inside $archive" >&2
  exit 1
fi

mkdir -p "$workdir/bundle/server"
cp "$binary" "$workdir/bundle/server/sense"
chmod +x "$workdir/bundle/server/sense"

sed -e "s|@VERSION@|$version|g" \
    -e "s|@PLATFORM@|$platform|g" \
    "$template" > "$workdir/bundle/manifest.json"

mkdir -p "$outdir"
outdir="$(cd "$outdir" && pwd)"
bundle="$outdir/sense_${version}_${os}_${arch}.mcpb"
rm -f "$bundle"

(cd "$workdir/bundle" && zip -qr "$bundle" manifest.json server)

echo "$bundle"
