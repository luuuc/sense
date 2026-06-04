package embed

import (
	"context"
	"testing"
)

// TestONNXEmbedderCloseNilSession covers Close's guard for an embedder whose
// session was never created (the zero value). It needs no ONNX runtime — the
// session.Destroy path is exercised by the onnx_integration round-trip.
func TestONNXEmbedderCloseNilSession(t *testing.T) {
	var e ONNXEmbedder // session is nil
	if err := e.Close(); err != nil {
		t.Errorf("Close on nil session = %v, want nil", err)
	}
}

// TestEmbedCanceledContext covers Embed's per-batch context check: a cancelled
// context aborts before any ONNX session call, so it runs without a runtime.
func TestEmbedCanceledContext(t *testing.T) {
	var e ONNXEmbedder // session is nil — never reached because ctx is cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := e.Embed(ctx, []EmbedInput{{QualifiedName: "x"}})
	if err == nil {
		t.Fatal("Embed with cancelled context: expected error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("Embed error = %v, want context.Canceled", err)
	}
}

// TestEmbedEmptyInputs covers the no-input fast path: the batch loop never runs,
// so an empty, non-nil result returns without touching the ONNX session.
func TestEmbedEmptyInputs(t *testing.T) {
	var e ONNXEmbedder
	out, err := e.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed(nil): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Embed(nil) = %v, want empty", out)
	}
}
