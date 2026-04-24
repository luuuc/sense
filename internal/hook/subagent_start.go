package hook

import (
	"context"
	"encoding/json"

	"github.com/luuuc/sense/internal/sqlite"
)

const subagentGuidance = "This project has a Sense index. Before using grep/glob, check if Sense MCP tools can answer your question:\n" +
	"- Symbol relationships → sense_graph\n" +
	"- Semantic code search → sense_search\n" +
	"- Impact analysis → sense_blast\n" +
	"- Project patterns → sense_conventions\n" +
	"Sense queries are faster and include structural context."

func handleSubagentStart(_ context.Context, _ json.RawMessage, _ *sqlite.Adapter, _ string) (any, error) {
	return &hookResponse{AdditionalContext: subagentGuidance}, nil
}
