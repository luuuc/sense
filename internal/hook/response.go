package hook

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
