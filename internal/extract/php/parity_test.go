package php

import (
	"encoding/json"
	"os"
	"testing"
)

// paritySymbol / parityEdge mirror the golden schema of the retired
// langspec extractor's baseline capture.
type paritySymbol struct {
	Name       string `json:"name"`
	Qualified  string `json:"qualified"`
	Kind       string `json:"kind"`
	Visibility string `json:"visibility"`
}

type parityEdge struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type parityBaseline struct {
	Symbols []paritySymbol `json:"symbols"`
	Edges   []parityEdge   `json:"edges"`
}

func loadParityBaseline(t *testing.T) parityBaseline {
	t.Helper()
	raw, err := os.ReadFile("testdata/langspec_parity_baseline.json")
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	var b parityBaseline
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	if len(b.Symbols) == 0 {
		t.Fatal("baseline has no symbols - capture is broken")
	}
	return b
}

func mustRunFile(t *testing.T, path string) *rec {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	em := &rec{}
	if err := (Extractor{}).Extract(parse(t, string(src)), src, path, em); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return em
}
