// Package languages blank-imports every language extractor so their
// init() hooks register with extract's package-level registry. The scan
// harness and the fixture test runner both import this package — one
// list of enabled languages, one place to forget a new one, caught at
// compile time rather than at runtime.
package languages

import (
	_ "github.com/luuuc/sense/internal/extract/erb"
	_ "github.com/luuuc/sense/internal/extract/golang"
	_ "github.com/luuuc/sense/internal/extract/python"
	_ "github.com/luuuc/sense/internal/extract/ruby"
	_ "github.com/luuuc/sense/internal/extract/rust"
	_ "github.com/luuuc/sense/internal/extract/tsjs"
)
