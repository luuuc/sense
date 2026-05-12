package hook

const toolSearchCmd = `ToolSearch("select:mcp__sense__sense_graph,mcp__sense__sense_search,mcp__sense__sense_blast,mcp__sense__sense_conventions,mcp__sense__sense_status")`

type hookResponse struct {
	AdditionalContext string `json:"additionalContext,omitempty"`
}

type messageResponse struct {
	Message string `json:"message,omitempty"`
}

type nudgeResponse struct {
	AdditionalContext string `json:"additionalContext,omitempty"`
	SystemMessage     string `json:"systemMessage,omitempty"`
}

func nudge(tip, context string) *nudgeResponse {
	return &nudgeResponse{
		SystemMessage:     tip,
		AdditionalContext: context,
	}
}
