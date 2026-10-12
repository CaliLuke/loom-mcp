package summaryeval

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	agRuntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Replay invokes only the production summary activity in a fresh scratch store.
func Replay(ctx context.Context, c Cut, in *agRuntime.SummarizeContextInput, client model.Client) (Side, error) {
	return replay(ctx, c, in, client, time.Now)
}

func replay(ctx context.Context, c Cut, in *agRuntime.SummarizeContextInput, client model.Client, now func() time.Time) (Side, error) {
	store, attempt, err := scratch(ctx, c, in)
	if err != nil {
		return Side{}, err
	}
	rt := agRuntime.New(agRuntime.WithRunStore(store), agRuntime.WithLogger(telemetry.NoopLogger{}))
	if err := rt.RegisterModel(in.ContextModel.ID, client); err != nil {
		return Side{}, err
	}
	start := now()
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(ctx, attempt), in)
	if err != nil {
		return Side{}, err
	}
	side := Side{Kind: out.Kind, Usage: out.Usage, Chunks: out.Chunks, UnsummarisedBytes: out.UnsummarisedBytes, LooseRefs: out.LooseRefs, Latency: now().Sub(start), Prompt: out.Prompt, Fingerprint: in.ContextFingerprint}
	side, err = collectCalls(ctx, store, in, side)
	if err != nil {
		return side, err
	}
	side, err = renderSide(ctx, c, in, out, store, side)
	if err != nil {
		return side, err
	}
	side.Bytes = len(side.Text)
	return side, nil
}

func responseText(resp *model.Response) string {
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

func messageText(msg *model.Message) string {
	var text strings.Builder
	for _, part := range msg.Parts {
		if p, ok := part.(model.TextPart); ok {
			text.WriteString(p.Text)
		}
	}
	return text.String()
}

func scratch(ctx context.Context, c Cut, in *agRuntime.SummarizeContextInput) (runstore.Store, int, error) {
	store := inmem.New()
	for _, h := range slices.Sorted(maps.Keys(c.record.Content)) {
		actual, err := store.Put(ctx, in.Scope, c.record.Content[h])
		if err != nil {
			return nil, 0, err
		}
		if actual != h {
			return nil, 0, fmt.Errorf("compaction: content hash mismatch %s", h)
		}
	}
	calls := summaryCalls(c)
	attempt := 1
	if len(calls) > 0 {
		attempt = calls[0].Record.Key.Attempt
		for seq := 1; seq < calls[0].Record.Key.Seq; seq++ {
			if _, err := store.NextSeq(ctx, in.Scope, in.RunID); err != nil {
				return nil, 0, err
			}
		}
	}
	return store, attempt, nil
}

func renderSide(ctx context.Context, c Cut, in *agRuntime.SummarizeContextInput, out *agRuntime.SummarizeContextOutput, store runstore.Store, side Side) (Side, error) {
	if out.Checkpoint != nil {
		side.Checkpoint = *out.Checkpoint
		source := func(ctx context.Context, h runstore.Hash) ([]byte, error) {
			return store.Get(ctx, in.Scope, h)
		}
		bytes, err := source(ctx, *out.Checkpoint)
		if err != nil {
			return side, err
		}
		var record index.CheckpointRecord
		if err := decode(bytes, &record); err != nil {
			return side, err
		}
		if record.Summary != nil {
			text, err := source(ctx, *record.Summary)
			if err != nil {
				return side, err
			}
			side.Text = string(text)
		}
		rendered, err := checkpoint.FromRecord(ctx, source, *out.Checkpoint, c.run.Entries, in.ViewRef.ChainPos)
		if err != nil {
			return side, err
		}
		rendered.Pinned = out.Pinned
		message, err := checkpoint.Render(rendered, in.Budget, checkpoint.BudgetLimits(in.Budget), nil)
		if err != nil {
			return side, err
		}
		side.RenderedBytes = len(messageText(message))
	}
	return side, nil
}

func collectCalls(ctx context.Context, store runstore.Store, in *agRuntime.SummarizeContextInput, side Side) (Side, error) {
	recorded, err := store.ListCalls(ctx, in.Scope, in.RunID)
	if err != nil {
		return side, err
	}
	for _, call := range recorded {
		started, err := callrecord.DecodeStarted(call.Started)
		if err != nil {
			return side, err
		}
		if started.Activity != callrecord.ActivitySummarize {
			return side, fmt.Errorf("compaction: non-summary scratch call")
		}
		evidence := Call{Seq: call.Key.Seq, Digest: started.Request.Request}
		if len(call.Completion) > 0 {
			done, err := callrecord.DecodeCompletion(call.Completion)
			if err != nil {
				return side, err
			}
			if done.Response != nil {
				data, err := store.Get(ctx, in.Scope, *done.Response)
				if err != nil {
					return side, err
				}
				response, err := callrecord.DecodeResponse(data)
				if err != nil {
					return side, err
				}
				evidence.Text = responseText(response)
			}
		}
		side.Calls = append(side.Calls, evidence)
		side.Text = evidence.Text
	}
	return side, nil
}
