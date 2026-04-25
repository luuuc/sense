package hook

const toolSearchCmd = `ToolSearch("select:mcp__sense__sense_graph,mcp__sense__sense_search,mcp__sense__sense_blast,mcp__sense__sense_conventions,mcp__sense__sense_status")`

type hookResponse struct {
	AdditionalContext string `json:"additionalContext,omitempty"`
}

type messageResponse struct {
	Message string `json:"message,omitempty"`
}

type denyResponse struct {
	Output denyOutput `json:"hookSpecificOutput"`
}

type denyOutput struct {
	Event    string `json:"hookEventName"`
	Decision string `json:"permissionDecision"`
	Reason   string `json:"permissionDecisionReason"`
}

func deny(reason string) *denyResponse {
	return &denyResponse{
		Output: denyOutput{
			Event:    "PreToolUse",
			Decision: "deny",
			Reason:   reason,
		},
	}
}

func advise(msg string) *hookResponse {
	return &hookResponse{AdditionalContext: msg}
}
