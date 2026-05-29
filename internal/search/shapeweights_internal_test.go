package search

import "testing"

// TestShapeWeightsIdentifierUnchanged asserts that an Identifier query
// reproduces the confidence ladder byte-for-byte at every bucket — the
// pitch's hard guarantee that today's behavior is preserved.
func TestShapeWeightsIdentifierUnchanged(t *testing.T) {
	for _, conf := range []float64{0.0, 0.3, 0.39, 0.4, 0.5, 0.59, 0.6, 0.9, 1.0} {
		wantKw, wantVec := fusionWeights(conf)
		gotKw, gotVec := shapeWeights(ShapeIdentifier, conf)
		if gotKw != wantKw || gotVec != wantVec {
			t.Errorf("shapeWeights(Identifier, %v) = (%v, %v), want fusionWeights = (%v, %v)",
				conf, gotKw, gotVec, wantKw, wantVec)
		}
	}
}

// TestShapeWeightsNaturalLanguageFloor asserts the vector weight never
// falls below the floor for an NL query, INCLUDING the low and very-low
// confidence buckets where fusionWeights alone would return 0.4 / 0.3.
func TestShapeWeightsNaturalLanguageFloor(t *testing.T) {
	tests := []struct {
		conf    float64
		wantKw  float64
		wantVec float64
	}{
		{0.0, 0.5, 0.5},  // very-low bucket: 0.3 → floored to 0.5
		{0.3, 0.5, 0.5},  // very-low bucket
		{0.4, 0.5, 0.5},  // low bucket: 0.4 → floored to 0.5
		{0.59, 0.5, 0.5}, // low bucket
		{0.6, 0.5, 0.5},  // high bucket: already 0.5, unchanged
		{1.0, 0.5, 0.5},  // high bucket
	}
	for _, tt := range tests {
		gotKw, gotVec := shapeWeights(ShapeNaturalLanguage, tt.conf)
		if gotVec < naturalLanguageVectorFloor {
			t.Errorf("shapeWeights(NaturalLanguage, %v) vector = %v, below floor %v",
				tt.conf, gotVec, naturalLanguageVectorFloor)
		}
		if gotKw != tt.wantKw || gotVec != tt.wantVec {
			t.Errorf("shapeWeights(NaturalLanguage, %v) = (%v, %v), want (%v, %v)",
				tt.conf, gotKw, gotVec, tt.wantKw, tt.wantVec)
		}
	}
}

// TestShapeWeightsMixedBalanced asserts Mixed is balanced regardless of
// confidence — neither leg dominates.
func TestShapeWeightsMixedBalanced(t *testing.T) {
	for _, conf := range []float64{0.0, 0.4, 0.6, 1.0} {
		gotKw, gotVec := shapeWeights(ShapeMixed, conf)
		if gotKw != 0.5 || gotVec != 0.5 {
			t.Errorf("shapeWeights(Mixed, %v) = (%v, %v), want (0.5, 0.5)", conf, gotKw, gotVec)
		}
	}
}
