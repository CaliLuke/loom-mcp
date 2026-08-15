package assistantapi

import (
	"context"
	"encoding/json"

	assistant "example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
)

// promptProvider implements the generated PromptProvider interface to
// serve dynamic prompts used by tests (e.g., "contextual_prompts").
type promptProvider struct{}

func (promptProvider) GetContextualPromptsPrompt(ctx context.Context, arguments json.RawMessage) (*mcpassistant.PromptsGetResult, error) {
	var payload struct {
		Context string `json:"context"`
	}
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &payload); err != nil {
			return nil, err
		}
	}
	text := "Dynamic contextual prompts"
	if payload.Context == "needs-elicitation" {
		result, err := mcpruntime.Elicit(ctx, mcpruntime.ElicitRequest{
			Mode:    "form",
			Message: "Provide prompt guidance.",
			RequestedSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"guidance": map[string]any{
						"type": "string",
					},
				},
				"required": []any{"guidance"},
			},
		})
		if err != nil {
			return nil, err
		}
		text = "Elicitation declined."
		if result != nil && result.Action == "accept" {
			if guidance, ok := result.Content["guidance"].(string); ok {
				text = guidance
			}
		}
	}
	return &mcpassistant.PromptsGetResult{
		Description: nil,
		Messages: []*mcpassistant.PromptMessage{
			{Role: "system", Content: &mcpassistant.MessageContent{Type: "text", Text: strPtr(text)}},
		},
	}, nil
}

// GetCodeReviewPrompt satisfies the generated provider when a static prompt is present.
func (promptProvider) GetCodeReviewPrompt(arguments json.RawMessage) (*mcpassistant.PromptsGetResult, error) {
	return &mcpassistant.PromptsGetResult{
		Description: strPtr("Code review guidance"),
		Messages: []*mcpassistant.PromptMessage{
			{Role: "system", Content: &mcpassistant.MessageContent{Type: "text", Text: strPtr("Review the provided code and suggest improvements.")}},
		},
	}, nil
}

func (promptProvider) GetFigmaImplementationPromptPrompt(ctx context.Context, arguments json.RawMessage) (*mcpassistant.PromptsGetResult, error) {
	var payload struct {
		ScreenTitle     string `json:"screen_title"`
		Framework       string `json:"framework"`
		DesignTokensURI string `json:"design_tokens_uri"`
		DPIJSON         string `json:"dpi_json"`
	}
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &payload); err != nil {
			return nil, err
		}
	}
	var spec assistant.DPISpec
	if payload.DPIJSON != "" {
		_ = json.Unmarshal([]byte(payload.DPIJSON), &spec)
	}
	screenTitle := payload.ScreenTitle
	if screenTitle == "" {
		screenTitle = spec.ScreenTitle
	}
	return &mcpassistant.PromptsGetResult{
		Description: strPtr("Figma implementation handoff"),
		Messages: []*mcpassistant.PromptMessage{
			{Role: "system", Content: &mcpassistant.MessageContent{Type: "text", Text: strPtr(FixtureImplementationPrompt(screenTitle, payload.Framework, payload.DesignTokensURI, &spec))}},
		},
	}, nil
}

func strPtr(s string) *string { return &s }
