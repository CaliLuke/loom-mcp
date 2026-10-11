package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type eligibleReceiptCall struct{ Name, ToolCallID, Payload string }

// receiptRecord stores only newly evicted evidence. The previous record owns
// older receipts; summary evidence is copied without changing its provenance.
func receiptRecord(ctx context.Context, store runstore.Store, scope string, source index.Source, entries []index.MessageEntry, view index.ViewRef) (runstore.Hash, error) {
	var previous index.CheckpointRecord
	var hash runstore.Hash
	if view.Checkpoint != nil {
		hash = *view.Checkpoint
		data, err := source(ctx, hash)
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(data, &previous, json.RejectUnknownMembers(true)); err != nil {
			return "", err
		}
	}
	if previous.Through >= view.TailFrom {
		return "", fmt.Errorf("checkpoint covers the selected tail")
	}
	if view.TailFrom <= previous.Through+1 {
		return hash, nil
	}
	lines, err := receiptLines(ctx, source, entries, previous.Through, view.TailFrom)
	if err != nil {
		return "", err
	}
	record := index.CheckpointRecord{Prev: view.Checkpoint, Summary: previous.Summary, SummaryRun: previous.SummaryRun, CitedUsers: previous.CitedUsers, Receipt: lines, Through: view.TailFrom - 1, Chain: entries[view.TailFrom-2].Chain}
	data, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	hash, err = store.Put(ctx, scope, data)
	if err != nil {
		return "", fmt.Errorf("store checkpoint: %w", err)
	}
	return hash, nil
}

// receiptLines constructs the base call and assistant receipts. K15 adds summary
// annotations separately. Only eligible calls and assistant text are sources;
// record/output/server-only content is never read.
func receiptLines(ctx context.Context, source index.Source, entries []index.MessageEntry, through, tailFrom index.MessageID) ([]index.ReceiptLine, error) {
	hidden := hiddenCalls(entries)
	var lines []index.ReceiptLine
	for _, entry := range entries {
		if entry.ID <= through {
			continue
		}
		if entry.ID >= tailFrom {
			break
		}
		if entry.Kind != index.Assistant && !(entry.Kind == index.Message && entry.Role == model.ConversationRoleAssistant) {
			continue
		}
		messages, err := receiptAssistantMessages(ctx, source, entry)
		if err != nil {
			return nil, err
		}
		calls, err := receiptEntryCalls(ctx, source, entry, messages)
		if err != nil {
			return nil, err
		}
		for _, call := range calls {
			if !hidden[call.ToolCallID] {
				lines = append(lines, index.ReceiptLine{Kind: "call", From: entry.ID, Through: entry.ID, ToolUseID: call.ToolCallID, Tool: call.Name, Text: receiptPrefix(call.Payload, checkpoint.ReceiptArgsBytes)})
			}
		}
		text := transcriptText(messages)
		if text != "" {
			lines = append(lines, index.ReceiptLine{Kind: "assistant", From: entry.ID, Through: entry.ID, Text: receiptPrefix(firstReceiptLine(text), checkpoint.MaxReceiptLineBytes)})
		}
	}
	return lines, nil
}

// receiptEntryCalls resolves eligible call data without constructing receipt lines.
func receiptEntryCalls(ctx context.Context, source index.Source, entry index.MessageEntry, messages []*model.Message) ([]eligibleReceiptCall, error) {
	if entry.Kind == index.Message {
		return messageReceiptCalls(messages)
	}
	calls := make([]eligibleReceiptCall, 0, len(entry.Calls))
	for _, hash := range entry.Calls {
		call, err := receiptCall(ctx, source, hash)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, nil
}

// messageReceiptCalls reads tool declarations held by seed and resume messages.
func messageReceiptCalls(messages []*model.Message) ([]eligibleReceiptCall, error) {
	var calls []eligibleReceiptCall
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, part := range message.Parts {
			var call model.ToolUsePart
			switch value := part.(type) {
			case model.ToolUsePart:
				call = value
			case *model.ToolUsePart:
				call = *value
			default:
				continue
			}
			args, err := json.Marshal(call.Input, json.Deterministic(true))
			if err != nil {
				return nil, err
			}
			calls = append(calls, eligibleReceiptCall{Name: call.Name, ToolCallID: call.ID, Payload: string(args)})
		}
	}
	return calls, nil
}

func receiptCall(ctx context.Context, source index.Source, hash runstore.Hash) (eligibleReceiptCall, error) {
	data, err := source(ctx, hash)
	if err != nil {
		return eligibleReceiptCall{}, err
	}
	var call struct {
		Name       string
		ToolCallID string
		Payload    rawjson.Message
	}
	if err := json.Unmarshal(data, &call); err != nil {
		return eligibleReceiptCall{}, err
	}
	return eligibleReceiptCall{Name: call.Name, ToolCallID: call.ToolCallID, Payload: string(call.Payload)}, nil
}

func hiddenCalls(entries []index.MessageEntry) map[string]bool {
	hidden := map[string]bool{}
	for _, entry := range entries {
		if entry.Structural != nil && entry.Structural.Kind == index.HideBookkeeping {
			for _, id := range entry.ToolUseIDs {
				hidden[id] = true
			}
		}
	}
	return hidden
}

// receiptAssistantMessages reads eligible assistant content for prose and stored
// message tool uses. Activity-owned calls use their separate eligible hashes.
func receiptAssistantMessages(ctx context.Context, source index.Source, entry index.MessageEntry) ([]*model.Message, error) {
	if entry.Hash == "" {
		return nil, nil
	}
	data, err := source(ctx, entry.Hash)
	if err != nil {
		return nil, err
	}
	var messages []*model.Message
	if entry.Kind == index.Message {
		var message *model.Message
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		messages = []*model.Message{message}
	} else if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func firstReceiptLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

func receiptPrefix(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

// prefixHidden and validTail use the same pure projection rules as RunRecord.
func prefixHidden(entries []index.MessageEntry) map[string]bool {
	return checkpoint.PrefixHidden(entries)
}

func validTail(entries []index.MessageEntry, tail index.MessageID) error {
	return checkpoint.ValidTail(entries, tail)
}
