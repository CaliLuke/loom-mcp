package runtime

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/summarize"
)

type summaryExecution struct {
	runtime   *Runtime
	input     *SummarizeContextInput
	output    *SummarizeContextOutput
	recording *CallRecording
	prepared  *summaryPreparation
	spec      prompt.PromptSpec
	plan      summarize.Plan
	limits    model.ContextLimits
	client    model.Client
	resolver  checkpoint.CitationResolver
}

// prepareSummaryExecution resolves one prompt version and plans all receipt annotations before sending.
func (r *Runtime) prepareSummaryExecution(ctx context.Context, in *SummarizeContextInput, out *SummarizeContextOutput, recording *CallRecording, registered model.Client, limits model.ContextLimits) (*summaryExecution, error) {
	prepared, err := r.prepareSummary(ctx, in)
	if err != nil {
		return nil, err
	}
	out.IndexHead = prepared.head
	spec, err := r.resolvePinnedSummary(ctx, in)
	if err != nil {
		return nil, err
	}
	render, err := summaryTemplate(spec)
	if err != nil {
		return nil, err
	}
	out.Prompt = &prompt.PromptRef{ID: spec.ID, Version: spec.Version}
	plan, err := summarize.PlanChunks(prepared.evidence, summarize.ChunkOptions{MaxSummaryChunks: in.ContextSettings.MaxSummaryChunks, MaxSummarisedResultTokens: in.ContextSettings.MaxSummarisedResultTokens, Limits: limits, MaxSummaryTokens: in.Budget.MaxSummaryTokens, PromptFor: func(previous string, chunk summarize.Chunk) (string, error) {
		return render(summaryData(in, previous, chunk))
	}})
	if err != nil {
		return nil, err
	}
	prepared.record.Receipt, err = summarize.AnnotateReceipts(prepared.record.Receipt, plan)
	if err != nil {
		return nil, err
	}
	for _, span := range plan.Unsummarised {
		out.UnsummarisedBytes += span.End - span.Start
	}
	client := &callInfoClientWrapper{store: r.RunStore, recording: recording, info: model.CallInfo{AgentID: string(in.AgentID), RunID: in.RunID, RootRunID: in.Scope, SessionID: in.SessionID, TurnID: in.TurnID, ModelID: in.ContextModel.ID, Attempt: engine.ActivityAttemptFromContext(ctx)}, inner: &modelInterceptedClient{inner: &callRecordingClient{inner: registered, recording: recording}}}
	resolver, err := r.summaryResolver(ctx, in, prepared)
	if err != nil {
		return nil, err
	}

	return &summaryExecution{runtime: r, input: in, output: out, recording: recording, prepared: prepared, spec: spec, plan: plan, limits: limits, client: client, resolver: resolver}, nil
}

// fold validates each recorded response before it becomes the next running summary.
func (e *summaryExecution) fold(ctx context.Context, previous string, chunk summarize.Chunk) (string, error) {
	in := e.input
	content, err := e.runtime.PromptRegistry.RenderResolved(ctx, e.spec, prompt.Scope{SessionID: in.SessionID, Labels: in.Labels}, summaryData(in, previous, chunk))
	if err != nil {
		return "", err
	}
	req := summarize.RenderChunk(content.Text, previous, chunk)
	req.ModelClass, req.RunID = in.ContextModel.Class, in.RunID
	req.PromptRefs = []prompt.PromptRef{content.Ref}
	facts, err := contextmeasure.Measure(req, e.limits, nil)
	if err != nil {
		return "", err
	}
	ceiling := e.plan.Budget - e.plan.Budget/10 - boolInt(e.plan.Budget%10 != 0)
	if contextmeasure.Estimate(contextmeasure.Rate{}, 0, facts.TextBytes, facts.NonText) > ceiling {
		return "", &summarize.Rejection{Kind: "summariser_overflow"}
	}
	response, callErr, err := summaryCall(ctx, e.client, e.recording, req)
	if err != nil {
		return "", err
	}
	if response == nil {
		return "", &summarize.Rejection{Kind: summaryFailureKind(callErr)}
	}
	text := summaryResponseText(response)
	candidate, validation, err := e.validateResponse(ctx, response, text)
	e.output.LooseRefs = validation.LooseRefs
	e.output.Usage = addSummaryUsage(e.output.Usage, response.Usage)
	if err != nil {
		return "", err
	}
	if callErr != nil {
		return "", &summarize.Rejection{Kind: summaryFailureKind(callErr)}
	}
	e.prepared.record = candidate
	e.prepared.record.CitedUsers = summaryCitedUsers(validation.CitedUsers, in.Budget)
	e.output.Chunks++
	return text, nil
}

// validateResponse builds only a read-only candidate for ordered K15 validation.
func (e *summaryExecution) validateResponse(ctx context.Context, response *model.Response, text string) (index.CheckpointRecord, summarize.Validation, error) {
	in := e.input
	keys := e.recording.Keys()
	candidate := e.prepared.record
	candidate.Calls = append(candidate.Calls, keys[len(keys)-1])
	candidate.Prompt = e.output.Prompt
	validation, err := summarize.Validate(summarize.ValidationInput{Text: text, StopReason: response.StopReason, Unfinished: response.OutputLimited, DefaultPrompt: e.spec.Template == defaultContextSummaryPrompt, SummaryBytes: e.prepared.allowance, ReplacedTokens: e.prepared.replaced, CurrentPos: in.ViewRef.ChainPos, To: in.To, Resolver: e.resolver, RenderedCheckpoint: func(text string) (int, error) {
		return e.prepared.estimate(ctx, in, candidate, text, e.resolver)
	}})
	return candidate, validation, err
}

// run stores a checkpoint only after every planned fold passes.
func (e *summaryExecution) run(ctx context.Context) error {
	if len(e.plan.Chunks) == 0 {
		return &summarize.Rejection{Kind: "empty"}
	}
	previous := e.prepared.evidence.Previous
	for _, chunk := range e.plan.Chunks {
		next, err := e.fold(ctx, previous, chunk)
		if err != nil {
			return err
		}
		previous = next
	}
	hash, pins, err := e.prepared.put(ctx, e.runtime.RunStore, e.input, previous)
	if err != nil {
		return err
	}
	e.output.Checkpoint, e.output.Pinned = &hash, pins
	return nil
}

func (r *Runtime) summaryResolver(ctx context.Context, in *SummarizeContextInput, prepared *summaryPreparation) (checkpoint.CitationResolver, error) {
	resolver := recall.NewCitationResolver(ctx, prepared.source, prepared.entries, in.To-1)
	if in.ViewRef.ChainPos > 0 {
		chain, err := r.continuationChain(ctx, in.Scope, in.RunID)
		if err != nil {
			return nil, err
		}
		if chain != nil {
			resolver = chain.Citations(ctx, prepared.source, prepared.entries, in.To-1, prepared.record.CitedUsers)
		}
	}
	return resolver, nil
}
