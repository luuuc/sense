package search

import (
	"math"
	"testing"
)

func TestNormalizeScoresSingleResult(t *testing.T) {
	results := []Result{{Score: 0.016}}
	normalizeScores(results)
	if results[0].Score != 1.0 {
		t.Errorf("single result score = %f, want 1.0", results[0].Score)
	}
}

func TestNormalizeScoresAllTied(t *testing.T) {
	results := []Result{{Score: 0.02}, {Score: 0.02}, {Score: 0.02}}
	normalizeScores(results)
	for i, r := range results {
		if r.Score != 1.0 {
			t.Errorf("tied result[%d] score = %f, want 1.0", i, r.Score)
		}
	}
}

func TestNormalizeScoresNormalSpread(t *testing.T) {
	results := []Result{
		{Score: 0.030},
		{Score: 0.020},
		{Score: 0.010},
	}
	normalizeScores(results)

	if math.Abs(results[0].Score-1.0) > 1e-9 {
		t.Errorf("max score = %f, want 1.0", results[0].Score)
	}
	if math.Abs(results[2].Score-0.0) > 1e-9 {
		t.Errorf("min score = %f, want 0.0", results[2].Score)
	}
	if math.Abs(results[1].Score-0.5) > 1e-9 {
		t.Errorf("mid score = %f, want 0.5", results[1].Score)
	}
}

func TestNormalizeScoresEmpty(t *testing.T) {
	normalizeScores(nil)
}
