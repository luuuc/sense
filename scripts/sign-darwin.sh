#!/bin/sh
# GoReleaser post-build hook: stamp darwin binaries with a stable
# code-signing identifier. The cross-linker's ad-hoc signature identifies
# the binary as "a.out"; quill re-signs it (still ad-hoc, no certificate)
# so `codesign -dv` reports com.github.luuuc.sense. This is also the slot
# where a Developer ID certificate drops in later (swap --ad-hoc for
# --p12 via QUILL_SIGN_P12) without touching the pipeline shape.
#
# quill is mounted into the goreleaser-cross container by release.yml.
set -eu

BINARY="$1"
GOOS="$2"

[ "$GOOS" = "darwin" ] || exit 0

quill sign --ad-hoc --identity com.github.luuuc.sense -q "$BINARY"
