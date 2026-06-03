package embed

import "testing"

// TestONNXEmbedderCloseNilSession covers Close's guard for an embedder whose
// session was never created (the zero value). It needs no ONNX runtime — the
// session.Destroy path is exercised by the onnx_integration round-trip.
func TestONNXEmbedderCloseNilSession(t *testing.T) {
	var e ONNXEmbedder // session is nil
	if err := e.Close(); err != nil {
		t.Errorf("Close on nil session = %v, want nil", err)
	}
}
