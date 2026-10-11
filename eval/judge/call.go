package judge

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// call owns a request's correction history; previously sent requests stay immutable.
func (j *Judge) call(ctx context.Context, req *model.Request, in Input, corrections int) (*Result, error) {
	result := &Result{Truncated: in.Truncated}
	for attempt := 0; attempt <= corrections; attempt++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := j.checkSize(req); err != nil {
			return result, err
		}
		result.Corrections = attempt
		started := time.Now()
		response, err := j.cfg.Client.Complete(ctx, req)
		result.Latency += time.Since(started)
		var rejected *model.OutputValidationError
		if err != nil {
			if !errors.As(err, &rejected) {
				return result, err
			}
			if usage := rejected.Usage(); usage != nil {
				addUsage(&result.Usage, *usage)
			}
		} else {
			if response != nil {
				addUsage(&result.Usage, response.Usage)
			}
			result.Judgments, err = decode(response, in.Claims)
			if err == nil {
				return result, nil
			}
		}
		if attempt == corrections {
			return result, fmt.Errorf("%w: %w", ErrInvalidOutput, err)
		}
		next := *req
		next.Messages = append(append([]*model.Message(nil), req.Messages...), correction(response, rejected, err)...)
		req = &next
	}
	panic("unreachable correction loop")
}

// correction never exposes content rejected by a validated model client.
func correction(response *model.Response, rejected *model.OutputValidationError, diagnostic error) []*model.Message {
	if rejected != nil {
		return []*model.Message{textMessage(model.ConversationRoleUser, fmt.Sprintf("Correct the rejected output (%s). Call %s with every claim and a valid label and rationale.", rejected.Kind(), toolName))}
	}
	if response == nil || len(response.ToolCalls) == 0 {
		messages := make([]*model.Message, 0, 2)
		if response != nil {
			for _, message := range response.Content {
				messages = append(messages, &message)
			}
		}
		return append(messages, textMessage(model.ConversationRoleUser, "Correct the output: "+diagnostic.Error()))
	}
	assistant := &model.Message{Role: model.ConversationRoleAssistant}
	result := &model.Message{Role: model.ConversationRoleUser}
	for i, call := range response.ToolCalls {
		input, err := model.DecodeSchemaJSON(call.Payload)
		if err != nil {
			// Malformed JSON (including duplicate members) cannot be embedded as raw
			// JSON in a request. A string retains the exact invalid payload for repair.
			input = string(call.Payload)
			if !utf8.Valid(call.Payload) {
				input = "Invalid UTF-8 JSON (base64): " + base64.StdEncoding.EncodeToString(call.Payload)
			}
		}
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("judge-correction-%d", i+1)
		}
		assistant.Parts = append(assistant.Parts, model.ToolUsePart{ID: id, Name: string(call.Name), Input: input})
		result.Parts = append(result.Parts, model.ToolResultPart{ToolUseID: id, Content: diagnostic.Error(), IsError: true})
	}
	return []*model.Message{assistant, result}
}

func addUsage(total *model.TokenUsage, delta model.TokenUsage) {
	total.Model = delta.Model
	total.ModelClass = delta.ModelClass
	total.InputTokens += delta.InputTokens
	total.OutputTokens += delta.OutputTokens
	total.CacheReadTokens += delta.CacheReadTokens
	total.CacheWriteTokens += delta.CacheWriteTokens
	total.TotalTokens = total.InputTokens + total.OutputTokens + total.CacheReadTokens + total.CacheWriteTokens
}
