// Package languages blank-imports every language extractor so their
// init() hooks register with extract's package-level registry. The scan
// harness and the fixture test runner both import this package — one
// list of enabled languages, one place to forget a new one, caught at
// compile time rather than at runtime.
package languages

// Language packages are added as they land. Until the first extractor
// lands (Ruby), this file is intentionally empty — the fixture test
// runner will iterate over an empty set of languages and pass.
