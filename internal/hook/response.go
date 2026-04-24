package hook

type hookResponse struct {
	AdditionalContext string `json:"additionalContext,omitempty"`
}

type messageResponse struct {
	Message string `json:"message,omitempty"`
}
