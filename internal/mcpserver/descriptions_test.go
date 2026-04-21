package mcpserver

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestToolDescriptionsContainRoutingKeywords(t *testing.T) {
	tools := []mcp.Tool{
		searchTool(),
		graphTool(),
		blastTool(),
		conventionsTool(),
		statusTool(),
	}

	routingPhrases := []string{
		"instead of",
		"use this",
		"Use this",
		"prefer",
	}

	for _, tool := range tools {
		t.Run(tool.Name, func(t *testing.T) {
			if len(tool.Description) < 100 {
				t.Errorf("description is %d chars, want >= 100", len(tool.Description))
			}

			hasRouting := false
			for _, phrase := range routingPhrases {
				if strings.Contains(tool.Description, phrase) {
					hasRouting = true
					break
				}
			}
			if !hasRouting {
				t.Errorf("description lacks routing keywords (expected one of %v)", routingPhrases)
			}
		})
	}
}

func TestParameterDescriptionsNonEmpty(t *testing.T) {
	tools := []mcp.Tool{
		searchTool(),
		graphTool(),
		blastTool(),
		conventionsTool(),
	}

	for _, tool := range tools {
		t.Run(tool.Name, func(t *testing.T) {
			for name, prop := range tool.InputSchema.Properties {
				propMap, ok := prop.(map[string]any)
				if !ok {
					t.Errorf("parameter %q: properties entry is not a map", name)
					continue
				}
				desc, _ := propMap["description"].(string)
				if len(desc) < 10 {
					t.Errorf("parameter %q: description is %d chars, want >= 10", name, len(desc))
				}
			}
		})
	}
}
