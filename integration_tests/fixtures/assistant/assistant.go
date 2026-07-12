package assistantapi

import (
	"context"
	"encoding/json"

	assistant "example.com/assistant/gen/assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	"github.com/CaliLuke/loom/clue/log"
)

// assistant service example implementation.
// The example methods log the requests and return zero values.
type assistantsrvc struct{}

// NewAssistant returns the assistant service implementation.
func NewAssistant() assistant.Service {
	return &assistantsrvc{}
}

// List available documents
func (s *assistantsrvc) ListDocuments(ctx context.Context) (res *assistant.Documents, err error) {
	res = &assistant.Documents{Items: []string{"Design system overview", "Checkout implementation notes"}}
	log.Printf(ctx, "assistant.list_documents")
	return
}

// Return system info
func (s *assistantsrvc) SystemInfo(ctx context.Context) (res *assistant.SystemInfoResult, err error) {
	name := "assistant-itest"
	version := "1.0.0"
	res = &assistant.SystemInfoResult{Name: &name, Version: &version}
	log.Printf(ctx, "assistant.system_info")
	return
}

// Return conversation history with optional query params
func (s *assistantsrvc) ConversationHistory(ctx context.Context, p *assistant.ConversationHistoryPayload) (res *assistant.ConversationHistoryResult, err error) {
	res = &assistant.ConversationHistoryResult{Items: []string{"User asked for a Figma implementation plan", "Assistant generated a DPI spec"}}
	log.Printf(ctx, "assistant.conversation_history")
	return
}

// Return a fake Figma design system summary for implementation validation
func (s *assistantsrvc) FigmaDesignSystem(ctx context.Context) (res *assistant.DesignSystem, err error) {
	res = FixtureDesignSystem()
	log.Printf(ctx, "assistant.figma_design_system")
	return
}

// Generate context-aware prompts
func (s *assistantsrvc) GeneratePrompts(ctx context.Context, p *assistant.GeneratePromptsPayload) (res *assistant.PromptTemplates, err error) {
	res = &assistant.PromptTemplates{Templates: []string{
		"Focus on implementation fidelity.",
		"Translate the design into production UI.",
	}}
	log.Printf(ctx, "assistant.generate_prompts")
	return
}

// Build a Figma-style implementation handoff prompt from a generated DPI spec
func (s *assistantsrvc) BuildFigmaImplementationPrompt(ctx context.Context, p *assistant.BuildFigmaImplementationPromptPayload) (res *assistant.PromptTemplates, err error) {
	if p == nil {
		p = &assistant.BuildFigmaImplementationPromptPayload{}
	}
	var spec assistant.DPISpec
	if p.DpiJSON != "" {
		_ = json.Unmarshal([]byte(p.DpiJSON), &spec)
	}
	res = &assistant.PromptTemplates{
		Templates: []string{
			"Figma implementation handoff",
			FixtureImplementationPrompt(p.ScreenTitle, p.Framework, p.DesignTokensURI, &spec),
		},
	}
	log.Printf(ctx, "assistant.build_figma_implementation_prompt")
	return
}

// Send status notification to client
func (s *assistantsrvc) SendNotification(ctx context.Context, p *assistant.SendNotificationPayload) (err error) {
	log.Printf(ctx, "assistant.send_notification")
	return
}

// Analyze sentiment of text
func (s *assistantsrvc) AnalyzeSentiment(ctx context.Context, p *assistant.AnalyzeSentimentPayload) (res *assistant.AnalyzeSentimentResult, err error) {
	sentiment := "positive"
	res = &assistant.AnalyzeSentimentResult{Sentiment: &sentiment}
	log.Printf(ctx, "assistant.analyze_sentiment")
	return
}

// Extract keywords from text
func (s *assistantsrvc) ExtractKeywords(ctx context.Context, p *assistant.ExtractKeywordsPayload) (res *assistant.ExtractKeywordsResult, err error) {
	res = &assistant.ExtractKeywordsResult{Keywords: []string{"figma", "checkout", "implementation"}}
	log.Printf(ctx, "assistant.extract_keywords")
	return
}

// Summarize text
func (s *assistantsrvc) SummarizeText(ctx context.Context, p *assistant.SummarizeTextPayload) (res *assistant.SummarizeTextResult, err error) {
	if p != nil && p.Text == "needs-elicitation" {
		result, elicitErr := mcpruntime.Elicit(ctx, mcpruntime.ElicitRequest{
			Mode:    "form",
			Message: "Provide a summary for the requested text.",
			RequestedSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary": map[string]any{
						"type": "string",
					},
				},
				"required": []any{"summary"},
			},
		})
		if elicitErr != nil {
			return nil, elicitErr
		}
		summary := "Elicitation declined."
		if result != nil && result.Action == "accept" {
			if value, ok := result.Content["summary"].(string); ok {
				summary = value
			}
		}
		res = &assistant.SummarizeTextResult{Summary: &summary}
		log.Printf(ctx, "assistant.summarize_text.elicited")
		return res, nil
	}
	summary := "Implementation handoff generated."
	res = &assistant.SummarizeTextResult{Summary: &summary}
	log.Printf(ctx, "assistant.summarize_text")
	return
}

// Sample text through the connected MCP client.
func (s *assistantsrvc) SampleText(ctx context.Context, p *assistant.SampleTextPayload) (*assistant.SampleTextResult, error) {
	systemPrompt := ""
	if p.SystemPrompt != nil {
		systemPrompt = *p.SystemPrompt
	}
	result, err := mcpruntime.Sample(ctx, mcpruntime.SampleRequest{
		Messages:     []mcpruntime.SampleMessage{{Role: "user", Text: p.Prompt}},
		SystemPrompt: systemPrompt,
		MaxTokens:    p.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	return &assistant.SampleTextResult{
		Text:       result.Text,
		Model:      result.Model,
		StopReason: result.StopReason,
	}, nil
}

// Search knowledge base
func (s *assistantsrvc) Search(ctx context.Context, p *assistant.SearchPayload) (res *assistant.SearchResult, err error) {
	res = &assistant.SearchResult{Results: []string{"checkout-screen.md", "design-system.md"}}
	log.Printf(ctx, "assistant.search")
	return
}

// Search records with an optional query
func (s *assistantsrvc) SearchRecords(ctx context.Context, p *assistant.SearchRecordsPayload) (res *assistant.SearchRecordsResult, err error) {
	res = &assistant.SearchRecordsResult{Results: []string{"login-event.json", "profile-update.json"}}
	log.Printf(ctx, "assistant.search_records")
	return
}

// Execute code
func (s *assistantsrvc) ExecuteCode(ctx context.Context, p *assistant.ExecuteCodePayload) (res *assistant.ExecuteCodeResult, err error) {
	output := "ok"
	res = &assistant.ExecuteCodeResult{Output: &output}
	log.Printf(ctx, "assistant.execute_code")
	return
}

// Process batch of items
func (s *assistantsrvc) ProcessBatch(ctx context.Context, p *assistant.ProcessBatchPayload) (res *assistant.ProcessBatchResult, err error) {
	ok := true
	res = &assistant.ProcessBatchResult{OK: &ok}
	log.Printf(ctx, "assistant.process_batch")
	return
}

// Return multiple content items
func (s *assistantsrvc) MultiContent(ctx context.Context, p *assistant.MultiContentPayload) (res *assistant.MultiContentResult, err error) {
	result := "hello world!"
	res = &assistant.MultiContentResult{Result: &result}
	log.Printf(ctx, "assistant.multi_content")
	return
}

// Generate a deterministic implementation-ready DPI spec from a fake Figma
// frame
func (s *assistantsrvc) GenerateDpiSpec(ctx context.Context, p *assistant.GenerateDpiSpecPayload) (res *assistant.DPISpec, err error) {
	res = FixtureDPISpec(p)
	log.Printf(ctx, "assistant.generate_dpi_spec")
	return
}

// Dispatch an action encoded as a union payload.
func (s *assistantsrvc) DispatchAction(ctx context.Context, p *assistant.DispatchActionPayload) (res *assistant.DispatchActionResult, err error) {
	ack := "ok"
	res = &assistant.DispatchActionResult{Ack: ack}
	log.Printf(ctx, "assistant.dispatch_action")
	return
}

// Dispatch a command using a union payload with a non-default branch key.
func (s *assistantsrvc) DispatchCommand(ctx context.Context, p *assistant.DispatchCommandPayload) (res *assistant.DispatchCommandResult, err error) {
	ack := "ok"
	res = &assistant.DispatchCommandResult{Ack: ack}
	log.Printf(ctx, "assistant.dispatch_command")
	return
}

// Lookup projected runtime tool data.
func (s *assistantsrvc) ProjectedLookup(ctx context.Context, p *assistant.ProjectedLookupPayload) (res *assistant.ProjectedLookupResult, err error) {
	answer := "projected:" + p.Query
	source := "runtime-toolset"
	res = &assistant.ProjectedLookupResult{Answer: answer, Source: source}
	log.Printf(ctx, "assistant.projected_lookup")
	return
}

// Return projected runtime status without a payload.
func (s *assistantsrvc) ProjectedStatus(ctx context.Context) (res *assistant.ProjectedStatusResult, err error) {
	res = &assistant.ProjectedStatusResult{Status: "ready"}
	log.Printf(ctx, "assistant.projected_status")
	return
}
