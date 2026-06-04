package tsjs

// imports_test.go holds the dynamic-import and re-export extraction tests,
// matching the production split in imports.go.

import "testing"

func TestDynamicImport(t *testing.T) {
	r := parseTS(t, `const lazy = import("./module");
`, "app.ts")
	if findEdg(r, "", "./module", "imports") == nil {
		t.Error("missing imports edge from dynamic import")
	}
}

func TestReexportStatement(t *testing.T) {
	r := parseTS(t, `export { Button } from "./Button";
`, "index.ts")
	if findEdg(r, "", "./Button", "imports") == nil {
		t.Error("missing imports edge from re-export")
	}
	if findSym(r, "Button") == nil {
		t.Error("missing re-exported symbol Button")
	}
}

func TestStarReexport(t *testing.T) {
	r := parseTS(t, `export * from "./utils";
`, "index.ts")
	if findEdg(r, "", "./utils", "imports") == nil {
		t.Error("missing imports edge from star re-export")
	}
}

func TestDynamicImportEdge(t *testing.T) {
	r := parseTS(t, `async function loadModule() {
  const mod = await import("./utils");
}
`, "app.ts")
	foundImport := false
	for _, e := range r.edges {
		if string(e.Kind) == "imports" && e.TargetQualified == "./utils" {
			foundImport = true
		}
	}
	if !foundImport {
		t.Error("missing imports edge for dynamic import('./utils')")
	}
}

func TestReexportFromModule(t *testing.T) {
	r := parseTS(t, `export { default as Utils } from "./utils";
export { Config } from "./config";
`, "index.ts")
	foundUtils := false
	foundConfig := false
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			if e.TargetQualified == "./utils" {
				foundUtils = true
			}
			if e.TargetQualified == "./config" {
				foundConfig = true
			}
		}
	}
	if !foundUtils {
		t.Error("missing imports edge for re-export from ./utils")
	}
	if !foundConfig {
		t.Error("missing imports edge for re-export from ./config")
	}
}

func TestStaticImportNoEdge(t *testing.T) {
	// Static import statements don't produce imports edges in Tier-Basic;
	// only dynamic import() and re-exports do.
	r := parseTS(t, `import { Router } from "express";
import * as path from "path";
`, "app.ts")
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			t.Errorf("unexpected imports edge from static import: %v", e.TargetQualified)
		}
	}
}

func TestReexportWithAlias(t *testing.T) {
	r := parseTS(t, `export { default as Button } from "./button";
`, "index.ts")
	foundBtn := false
	for _, s := range r.symbols {
		if s.Qualified == "Button" {
			foundBtn = true
		}
	}
	if !foundBtn {
		t.Error("missing re-exported symbol Button")
	}
}

func TestStarReexportEdge(t *testing.T) {
	r := parseTS(t, `export * from "./utils";
`, "barrel.ts")
	foundImport := false
	for _, e := range r.edges {
		if string(e.Kind) == "imports" && e.TargetQualified == "./utils" {
			foundImport = true
		}
	}
	if !foundImport {
		t.Error("missing imports edge from star re-export")
	}
}

func TestReexportDefaultSkipped(t *testing.T) {
	r := parseTS(t, `export { default } from "./button";
`, "index.ts")
	for _, s := range r.symbols {
		if s.Name == "default" {
			t.Error("default export should be skipped in re-export symbol emission")
		}
	}
}

func TestDynamicImportInsideFunction(t *testing.T) {
	r := parseTS(t, `async function load() {
  const mod = await import("./heavy-module");
  return mod.default;
}
`, "loader.ts")
	if findSym(r, "load") == nil {
		t.Fatal("missing symbol load")
	}
	// Dynamic import should create an imports edge
	hasImport := false
	for _, e := range r.edges {
		if string(e.Kind) == "imports" && e.TargetQualified == "./heavy-module" {
			hasImport = true
		}
	}
	if !hasImport {
		t.Error("missing imports edge from dynamic import()")
	}
}

func TestReexportEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `export { Foo } from "./foo";`, &failAfter{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on re-export imports edge emit")
	}
}

func TestDynamicImportEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `async function f() { await import("./x"); }`, &failAfter{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on dynamic import edge emit")
	}
}

func TestReexportSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `export { Foo } from "./foo";`, &failAfter{symbolsLeft: 0, edgesLeft: 100})
	// Re-export tries to emit imports edge first (edgesLeft=100 -> ok),
	// then symbol (symbolsLeft=0 -> fails)
	if err == nil {
		t.Error("expected error on re-export symbol emit")
	}
}
