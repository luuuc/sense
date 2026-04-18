// Package grammars centralises every tree-sitter grammar Sense bundles
// into its binary. One Go file per grammar exposes a single constructor
// returning *tree_sitter.Language. Extractors import the grammars they
// need — they never import the upstream grammar modules directly.
//
// # Bundling mechanism
//
// The pitch (01-02) initially described grammars as "embedded via go:embed".
// The directive //go:embed only embeds files, not compilable C sources,
// so what actually happens is simpler: each upstream module
// (github.com/tree-sitter/tree-sitter-<lang>) ships its parser.c plus a
// CGO binding. The Go toolchain compiles and links those into the sense
// binary at `go build` time — the grammar ends up in the binary, just
// through the module+CGO path rather than //go:embed.
//
// # Version pinning
//
// go.mod is the single source of truth. When bumping a grammar:
//
//  1. go get github.com/tree-sitter/tree-sitter-<lang>@<version>
//  2. go test ./internal/grammars/ — catches ABI drift between runtime
//     and grammar (MIN_COMPATIBLE_LANGUAGE_VERSION).
//  3. go test ./internal/extract/... — catches node-name changes that
//     would shift extractor output.
//  4. If goldens drift intentionally, regenerate with
//     `go test ./internal/extract -update` and review the diff before
//     committing.
package grammars
