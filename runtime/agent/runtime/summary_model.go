package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/summarize"
)

// summaryCall retries only transient provider failures, with fresh outer CallInfo
// identity each time. Durable raw responses remain below output validation.
func summaryCall(ctx context.Context, client model.Client, recording *CallRecording, req *model.Request) (*model.Response, error, error) {
	for attempt := 0; attempt < 3; attempt++ {
		before := len(recording.Keys())
		resp, err := client.Complete(ctx, req)
		if finishErr := recording.Finish(context.WithoutCancel(ctx)); finishErr != nil {
			return nil, nil, errors.Join(err, finishErr)
		}
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if err == nil {
			return resp, nil, nil
		}
		keys := recording.Keys()
		if len(keys) == before {
			return nil, nil, err
		}
		var validation *model.OutputValidationError
		if errors.As(err, &validation) {
			raw, loadErr := summaryRecordedResponse(ctx, recording, keys[len(keys)-1])
			if loadErr != nil {
				return nil, nil, loadErr
			}
			return raw, err, nil
		}
		if !summaryTransient(err) || attempt == 2 {
			return nil, err, nil
		}
	}
	return nil, nil, fmt.Errorf("summary retry exhausted without outcome")
}

func summaryRecordedResponse(ctx context.Context, r *CallRecording, key runstore.CallKey) (*model.Response, error) {
	r.mu.Lock()
	hash := r.responses[key]
	r.mu.Unlock()
	if hash == "" {
		return nil, nil
	}
	data, err := r.store.Get(ctx, r.scope, hash)
	if err != nil {
		return nil, err
	}
	return callrecord.DecodeResponse(data)
}

func summaryTransient(err error) bool {
	var provider *model.ProviderError
	var timeout net.Error
	if errors.As(err, &provider) {
		if provider.Kind() == model.ProviderErrorKindContextOverflow {
			return false
		}
		if provider.Kind() == model.ProviderErrorKindRateLimited || provider.Kind() == model.ProviderErrorKindUnavailable || provider.HTTPStatus() >= 500 {
			return true
		}
	}

	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout())
}

func summaryFailureKind(err error) string {
	var provider *model.ProviderError
	if errors.As(err, &provider) && provider.Kind() == model.ProviderErrorKindContextOverflow {
		return "summariser_overflow"
	}
	return "summariser_error"
}

func summaryResponseText(resp *model.Response) string {
	var text []string
	for _, message := range resp.Content {
		if message.Role != model.ConversationRoleAssistant {
			continue
		}
		for _, part := range message.Parts {
			switch p := part.(type) {
			case model.TextPart:
				text = append(text, p.Text)
			case *model.TextPart:
				if p != nil {
					text = append(text, p.Text)
				}
			case model.CitationsPart:
				text = append(text, p.Text)
			case *model.CitationsPart:
				if p != nil {
					text = append(text, p.Text)
				}
			}
		}
	}
	return strings.Join(text, "\n")
}

func summaryData(in *SummarizeContextInput, previous string, chunk summarize.Chunk) summaryPromptData {
	previousReq := summarize.RenderChunk("", previous, summarize.Chunk{OutputTokens: chunk.OutputTokens})
	text := previousReq.Messages[1].Parts[0].(model.TextPart).Text
	_, text, _ = strings.Cut(text, "Previous summary:\n")
	text, _, _ = strings.Cut(text, "\nTranscript (quoted data):")
	evidenceReq := summarize.RenderChunk("", "", chunk)
	evicted := evidenceReq.Messages[1].Parts[0].(model.TextPart).Text
	_, evicted, _ = strings.Cut(evicted, "Transcript (quoted data):\n")
	return summaryPromptData{Previous: text, Evicted: evicted, Instructions: in.Settings.Instructions, Budget: chunk.OutputTokens}
}

func addSummaryUsage(a, b model.TokenUsage) model.TokenUsage {
	a.Model = firstNonEmpty(a.Model, b.Model)
	if a.ModelClass == "" {
		a.ModelClass = b.ModelClass
	}
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.TotalTokens += b.TotalTokens
	a.CacheReadTokens += b.CacheReadTokens
	a.CacheWriteTokens += b.CacheWriteTokens
	return a
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
