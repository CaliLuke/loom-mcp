package assistantapi

import (
	"context"
	"fmt"
	"strings"

	assistant "example.com/assistant/gen/assistant"
	"goa.design/clue/log"
)

// assistant service example implementation.
// The example methods return deterministic values for integration tests.
type assistantsrvc struct{}

// NewAssistant returns the assistant service implementation.
func NewAssistant() assistant.Service {
	return &assistantsrvc{}
}

// List available documents
func (s *assistantsrvc) ListDocuments(ctx context.Context) (res *assistant.Documents, err error) {
	res = &assistant.Documents{Items: []string{"design.md", "plan.md", "notes.txt"}}
	log.Printf(ctx, "assistant.list_documents")
	return
}

// Return system info
func (s *assistantsrvc) SystemInfo(ctx context.Context) (res *assistant.SystemInfoResult, err error) {
	res = &assistant.SystemInfoResult{Name: stringPtr("assistant-itest"), Version: stringPtr("1.0.0")}
	log.Printf(ctx, "assistant.system_info")
	return
}

// Return conversation history with optional query params
func (s *assistantsrvc) ConversationHistory(ctx context.Context, p *assistant.ConversationHistoryPayload) (res *assistant.ConversationHistoryResult, err error) {
	items := []string{"conversation-start"}
	if p != nil {
		if p.Limit != nil {
			items = append(items, fmt.Sprintf("limit=%d", *p.Limit))
		}
		if p.Flag != nil {
			items = append(items, fmt.Sprintf("flag=%t", *p.Flag))
		}
		if len(p.Nums) > 0 {
			nums := make([]string, 0, len(p.Nums))
			for _, n := range p.Nums {
				nums = append(nums, fmt.Sprintf("%g", n))
			}
			items = append(items, "nums="+strings.Join(nums, ","))
		}
	}
	res = &assistant.ConversationHistoryResult{Items: items}
	log.Printf(ctx, "assistant.conversation_history")
	return
}

// Generate context-aware prompts
func (s *assistantsrvc) GeneratePrompts(ctx context.Context, p *assistant.GeneratePromptsPayload) (res *assistant.PromptTemplates, err error) {
	templates := []string{"general"}
	if p != nil {
		templates = []string{
			fmt.Sprintf("context:%s", p.Context),
			fmt.Sprintf("task:%s", p.Task),
		}
	}
	res = &assistant.PromptTemplates{Templates: templates}
	log.Printf(ctx, "assistant.generate_prompts")
	return
}

// Send status notification to client
func (s *assistantsrvc) SendNotification(ctx context.Context, p *assistant.SendNotificationPayload) (err error) {
	log.Printf(ctx, "assistant.send_notification")
	return
}

// Analyze sentiment of text
func (s *assistantsrvc) AnalyzeSentiment(ctx context.Context, p *assistant.AnalyzeSentimentPayload) (res *assistant.AnalyzeSentimentResult, err error) {
	sentiment := "neutral"
	if p != nil {
		text := strings.ToLower(p.Text)
		switch {
		case strings.Contains(text, "love"), strings.Contains(text, "great"), strings.Contains(text, "perfect"):
			sentiment = "positive"
		case strings.Contains(text, "hate"), strings.Contains(text, "bad"), strings.Contains(text, "terrible"):
			sentiment = "negative"
		}
	}
	res = &assistant.AnalyzeSentimentResult{Sentiment: stringPtr(sentiment)}
	log.Printf(ctx, "assistant.analyze_sentiment")
	return
}

// Extract keywords from text
func (s *assistantsrvc) ExtractKeywords(ctx context.Context, p *assistant.ExtractKeywordsPayload) (res *assistant.ExtractKeywordsResult, err error) {
	keywords := []string{}
	if p != nil {
		for _, word := range strings.Fields(strings.ToLower(p.Text)) {
			word = strings.Trim(word, ".,!?")
			if word == "" {
				continue
			}
			keywords = append(keywords, word)
			if len(keywords) == 3 {
				break
			}
		}
	}
	res = &assistant.ExtractKeywordsResult{Keywords: keywords}
	log.Printf(ctx, "assistant.extract_keywords")
	return
}

// Summarize text
func (s *assistantsrvc) SummarizeText(ctx context.Context, p *assistant.SummarizeTextPayload) (res *assistant.SummarizeTextResult, err error) {
	summary := ""
	if p != nil {
		summary = p.Text
		if len(summary) > 24 {
			summary = summary[:24] + "..."
		}
	}
	res = &assistant.SummarizeTextResult{Summary: stringPtr(summary)}
	log.Printf(ctx, "assistant.summarize_text")
	return
}

// Search knowledge base
func (s *assistantsrvc) Search(ctx context.Context, p *assistant.SearchPayload) (res *assistant.SearchResult, err error) {
	limit := 3
	query := ""
	if p != nil {
		query = p.Query
		if p.Limit != nil && *p.Limit > 0 {
			limit = *p.Limit
		}
	}
	results := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		results = append(results, fmt.Sprintf("%s-result-%d", query, i+1))
	}
	res = &assistant.SearchResult{Results: results}
	log.Printf(ctx, "assistant.search")
	return
}

// Execute code
func (s *assistantsrvc) ExecuteCode(ctx context.Context, p *assistant.ExecuteCodePayload) (res *assistant.ExecuteCodeResult, err error) {
	output := ""
	if p != nil {
		output = fmt.Sprintf("%s:%s", p.Language, p.Code)
	}
	res = &assistant.ExecuteCodeResult{Output: stringPtr(output)}
	log.Printf(ctx, "assistant.execute_code")
	return
}

// Process batch of items
func (s *assistantsrvc) ProcessBatch(ctx context.Context, p *assistant.ProcessBatchPayload) (res *assistant.ProcessBatchResult, err error) {
	res = &assistant.ProcessBatchResult{OK: boolPtr(true)}
	log.Printf(ctx, "assistant.process_batch")
	return
}

// Return multiple content items
func (s *assistantsrvc) MultiContent(ctx context.Context, p *assistant.MultiContentPayload) (res *assistant.MultiContentResult, err error) {
	result := "default multi content"
	if p != nil {
		result = fmt.Sprintf("multi content count=%d", p.Count)
	}
	res = &assistant.MultiContentResult{Result: stringPtr(result)}
	log.Printf(ctx, "assistant.multi_content")
	return
}

func stringPtr(v string) *string {
	return &v
}

func boolPtr(v bool) *bool {
	return &v
}
