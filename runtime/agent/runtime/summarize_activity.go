package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/summarize"
)

type (
	// ContextModel identifies the registered summariser and its accepted evidence.
	ContextModel = api.ContextModel
	// SummaryModel is the activity's summary model configuration.
	SummaryModel = api.ContextModel
	// SummarizeContextInput binds one summary to its immutable cut.
	SummarizeContextInput = api.SummarizeContextInput
	// SummarizeContextOutput contains durable summary addresses and attempt evidence.
	SummarizeContextOutput = api.SummarizeContextOutput
)

// SummarizeContextActivityName identifies the registered summary activity.
func SummarizeContextActivityName(id agent.Ident) string {
	return string(id) + ".summarize_context"
}

// SummarizeContextActivity folds eligible evidence and stores a validated record
// before returning its address. It neither changes a view nor adopts call keys.
func (r *Runtime) SummarizeContextActivity(ctx context.Context, in *SummarizeContextInput) (out *SummarizeContextOutput, activityErr error) {
	if err := validateSummaryInput(ctx, in); err != nil {
		return nil, err
	}
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()
	recording := NewCallRecording(r.RunStore, in.Scope, index.ViewRef{}, nil, in.Step)
	recording.activity = callrecord.ActivitySummarize
	defer recording.finishSummaryActivity(context.WithoutCancel(ctx), &out, &activityErr)
	out = &SummarizeContextOutput{From: in.From, To: in.To, IndexHead: in.ViewRef.IndexHead}
	if err := r.summarize(ctx, in, out, recording); err != nil {
		var rejection *summarize.Rejection
		if errors.As(err, &rejection) {
			out.Kind = rejection.Kind
			return out, nil
		}
		return nil, err
	}
	return out, nil
}

func validateSummaryInput(ctx context.Context, in *SummarizeContextInput) error {
	if in == nil || in.RunID == "" || in.Scope == "" || in.AgentID == "" || in.ContextModel.ID == "" || in.From == 0 || in.From != max(index.MessageID(1), in.ViewRef.TailFrom) || in.To <= in.From || engine.ActivityAttemptFromContext(ctx) < 1 {
		return fmt.Errorf("summarize activity: invalid identity, attempt or cut")
	}
	return nil
}

func (r *Runtime) summarize(ctx context.Context, in *SummarizeContextInput, out *SummarizeContextOutput, recording *CallRecording) error {
	r.mu.RLock()
	registered, ok := r.models[in.ContextModel.ID]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("summarize activity: unregistered model %q", in.ContextModel.ID)
	}
	probe := &model.Request{ModelClass: in.ContextModel.Class, MaxTokens: in.Budget.MaxSummaryTokens}
	info, known := registered.(model.ContextInfo)
	if !known {
		return &summarize.Rejection{Kind: summaryLimitsUnknown}
	}
	limits, _, known := info.ContextLimits(ctx, probe)
	if !known || limits.ContextWindow <= 0 {
		return &summarize.Rejection{Kind: summaryLimitsUnknown}
	}
	execution, err := r.prepareSummaryExecution(ctx, in, out, recording, registered, limits)
	if err != nil {
		return err
	}
	return execution.run(ctx)
}

func (r *CallRecording) finishSummaryActivity(ctx context.Context, out **SummarizeContextOutput, activityErr *error) {
	if err := r.Finish(ctx); err != nil {
		*out = nil
		*activityErr = errors.Join(*activityErr, err)
	}
	if *out != nil {
		(*out).CallKeys = r.Keys()
		(*out).Facts = r.Facts()
	}
}
