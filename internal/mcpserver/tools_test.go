package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/profile"
)

// TestBlastSchemaAdvertisesRealDefault pins the min_confidence description to
// the constant the handler actually uses (profile.Defaults). The description
// once said "default 0.7" (copied from the CLI, which diverges by design)
// while the MCP default was 0.3 — agents that passed the documented value
// lost every sub-0.7 edge. A description is a contract; keep it tied to the
// value.
func TestBlastSchemaAdvertisesRealDefault(t *testing.T) {
	tool := blastTool()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	schema := string(raw)
	def := profile.DefaultParams()
	want := fmt.Sprintf("default %.1f", def.BlastMinConfidence)
	if !strings.Contains(schema, want) {
		t.Errorf("blast min_confidence description must state %q (the real MCP default); schema: %s", want, schema)
	}
	if strings.Contains(schema, "default 0.7") {
		t.Error("blast schema must not advertise the CLI's 0.7 default")
	}
}
