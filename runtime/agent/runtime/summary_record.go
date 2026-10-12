package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// renderCandidate uses an in-memory overlay; rejecting a summary writes no
// checkpoint record. The exact same encoding is subsequently Put on success.
func (p *summaryPreparation) renderCandidate(ctx context.Context, in *SummarizeContextInput, record index.CheckpointRecord, text string) (*model.Message, []index.MessageID, error) {
	summary := summaryHash([]byte(text))
	record.Summary = &summary
	data, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return nil, nil, err
	}
	hash := summaryHash(data)
	source := func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		if h == summary {
			return []byte(text), nil
		}
		if h == hash {
			return data, nil
		}
		return p.source(ctx, h)
	}
	candidate, err := checkpoint.FromRecord(ctx, source, hash, p.entries, 0)
	if err != nil {
		return nil, nil, err
	}
	candidate.Pinned = checkpoint.SelectPins(candidate.Users, in.Budget, checkpoint.BudgetLimits(in.Budget))
	message, err := checkpoint.Render(candidate, in.Budget, checkpoint.BudgetLimits(in.Budget), nil)
	return message, candidate.Pinned, err
}

func (p *summaryPreparation) estimate(ctx context.Context, in *SummarizeContextInput, record index.CheckpointRecord, text string, resolver checkpoint.CitationResolver) (int, error) {
	// Size validation precedes citation validation: unknown/non-user citations
	// contribute no canonical index line here; their rejection is checked next.
	var lines []checkpoint.UserLine
	seen := map[checkpoint.OrderKey]bool{}
	for _, cite := range checkpoint.ParseCitations(text) {
		key := checkpoint.OrderKey{Pos: cite.Pos, ID: cite.ID}
		if seen[key] {
			continue
		}
		seen[key] = true
		target, found, err := resolver.Resolve(key)
		if err != nil {
			return 0, err
		}
		if found && resolver.Behind(key) && target.Role == model.ConversationRoleUser && (target.Kind == index.Message || target.Kind == index.Clarification) {
			lines = append(lines, target.Line)
		}
	}
	record.CitedUsers = summaryCitedUsers(lines, in.Budget)
	message, _, err := p.renderCandidate(ctx, in, record, text)
	if err != nil {
		return 0, err
	}
	return summaryEstimate([]*model.Message{message}, in.Rate)
}

func (p *summaryPreparation) put(ctx context.Context, store runstore.Store, in *SummarizeContextInput, text string) (runstore.Hash, []index.MessageID, error) {
	_, pins, err := p.renderCandidate(ctx, in, p.record, text)
	if err != nil {
		return "", nil, err
	}
	summary, err := store.Put(ctx, in.Scope, []byte(text))
	if err != nil {
		return "", nil, err
	}
	if summary != summaryHash([]byte(text)) {
		return "", nil, fmt.Errorf("summary store returned incorrect hash")
	}
	p.record.Summary = &summary
	data, err := json.Marshal(p.record, json.Deterministic(true))
	if err != nil {
		return "", nil, err
	}
	hash, err := store.Put(ctx, in.Scope, data)
	if err != nil {
		return "", nil, fmt.Errorf("store summary checkpoint: %w", err)
	}
	if hash != summaryHash(data) {
		return "", nil, fmt.Errorf("summary record store returned incorrect hash")
	}
	return hash, pins, nil
}

func summaryCitedUsers(lines []checkpoint.UserLine, budget index.RenderBudget) []index.IndexLine {
	remaining := max(0, budget.KeepUserTokens*checkpoint.BytesPerToken)
	var result []index.IndexLine
	for _, line := range lines {
		cost := len(checkpoint.Label(line.ID, line.Origin, 0)) + len(line.Text) + len("[, first line] \n")
		if cost > remaining {
			break
		}
		remaining -= cost
		result = append(result, index.IndexLine{ID: line.ID, Origin: line.Origin, Text: line.Text})
	}
	return result
}

func summaryEstimate(messages []*model.Message, rate contextmeasure.Rate) (int, error) {
	facts, err := contextmeasure.Measure(&model.Request{Messages: messages}, model.ContextLimits{}, nil)
	if err != nil {
		return 0, err
	}
	return contextmeasure.Estimate(rate, 0, facts.TextBytes, facts.NonText), nil
}

func summaryHash(data []byte) runstore.Hash {
	return runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
}
