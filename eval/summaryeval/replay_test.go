package summaryeval

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	agRuntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureStore struct {
	runstore.Store
	content map[runstore.Hash][]byte
}

func TestRecordedReplay(t *testing.T) {
	rec := recordedFixture(t)
	cut := Cuts(rec)[0]
	verification, err := Verify(t.Context(), cut)
	require.NoError(t, err)
	assert.Equal(t, "verified", verification.Status)
	assert.Equal(t, "old fact", cut.Old.Text)
	in, err := Reconstruct(cut)
	require.NoError(t, err)
	assert.Equal(t, 9, in.Step)
	assert.Equal(t, "turn", in.TurnID)
	assert.Empty(t, in.ViewRef.Pending)
	script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("old fact"), agenttest.Usage(10, 5)))
	require.NoError(t, err)
	script.WithContextLimits(model.ContextLimits{ContextWindow: 200000})
	side, err := Replay(t.Context(), cut, in, script.Client())
	require.NoError(t, err)
	assert.Equal(t, cut.Record, side.Checkpoint)
	require.Len(t, side.Calls, 1)
	assert.Equal(t, 7, side.Calls[0].Seq)
	assert.Len(t, script.Requests(), 1)
	require.NoError(t, script.Err())
	t.Run("template mismatch", func(t *testing.T) {
		settings := *rec.Runs[0].ContextSettings
		settings.Prompt.Template = putFixture(t, rec, "changed {{.Evicted}}")
		rec.Runs[0].ContextSettings = &settings
		v, err := Verify(t.Context(), Cuts(rec)[0])
		require.NoError(t, err)
		assert.Equal(t, "mismatch", v.Status)
		assert.Equal(t, "request_digest", v.Field)
		assert.Equal(t, 7, v.FirstCall)
	})
}

func TestUnverifiable(t *testing.T) {
	for _, reason := range []string{"continued_run_citations", "no_calls", "view_mismatch", "recorded_provider_error"} {
		t.Run(reason, func(t *testing.T) {
			rec := recordedFixture(t)
			switch reason {
			case "continued_run_citations":
				rec.Content[*rec.Runs[0].Checkpoints[0].Record.Summary] = []byte("fact [r1 message 1]")
				rec.Runs[0].Pos = 2
			case "no_calls":
				rec.Runs[0].Calls = nil
			case "view_mismatch":
				rec.Runs[0].Events[0] = testEvent(t, hooks.PlannerStep, &hooks.PlannerStepEvent{Step: 8, ViewRef: index.ViewRef{TailFrom: 2}})
			case "recorded_provider_error":
				rec.Runs[0].Calls[0].Completion.Error = &callrecord.CallError{Kind: "provider", Message: "failed"}
			}
			v, err := Verify(t.Context(), Cuts(rec)[0])
			require.NoError(t, err)
			assert.Equal(t, "unverifiable", v.Status)
			assert.Equal(t, reason, v.Reason)
		})
	}
}

func TestVerificationGuards(t *testing.T) {
	for _, tt := range []struct {
		name, status, field, reason string
		change                      func(*testing.T, *Cut)
	}{
		{name: "checkpoint", status: "mismatch", field: "checkpoint", change: func(t *testing.T, c *Cut) {
			c.Record = contentHash([]byte("another checkpoint"))
		}},
		{name: "kind", status: "mismatch", field: "kind", change: func(t *testing.T, c *Cut) {
			c.Old.Kind = "no_saving"
		}},
		{name: "previous checkpoint", status: "unverifiable", reason: "view_mismatch", change: func(t *testing.T, c *Cut) {
			var step hooks.PlannerStepEvent
			require.NoError(t, decode(c.run.Events[0].Payload, &step))
			step.ViewRef.Checkpoint = new(contentHash([]byte("another predecessor")))
			c.run.Events[0] = testEvent(t, hooks.PlannerStep, &step)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cut := Cuts(recordedFixture(t))[0]
			tt.change(t, &cut)
			v, err := Verify(t.Context(), cut)
			require.NoError(t, err)
			assert.Equal(t, tt.status, v.Status)
			assert.Equal(t, tt.field, v.Field)
			assert.Equal(t, tt.reason, v.Reason)
		})
	}
}

func TestReplayRejectsCorruptContent(t *testing.T) {
	rec := recordedFixture(t)
	// Corrupt an otherwise unused blob so downstream decoding cannot mask a
	// missing content-address integrity check.
	hash := putFixture(t, rec, "original extra content")
	rec.Content[hash] = []byte("corrupted extra content")
	cut := Cuts(rec)[0]
	_, err := Verify(t.Context(), cut)
	require.ErrorContains(t, err, "content hash mismatch")
	assert.Contains(t, err.Error(), string(hash))
}

func TestReconstructOverflowFacts(t *testing.T) {
	facts := contextmeasure.RequestFacts{Call: runstore.CallKey{RunID: "run", Seq: 6, Attempt: 1}, Model: "planner", ContextWindow: 200000, ViewBearing: true, TextBytes: 6000, InputTokens: 1000, Outcome: contextmeasure.OutcomeOverflow}
	rec := recordedFixtureWith(t, "", &facts)
	cut := Cuts(rec)[0]
	in, err := Reconstruct(cut)
	require.NoError(t, err)
	// The fixture invokes production with ObserveEffective + SummaryRate, as
	// recoverOverflow does before the later threshold summary.
	assert.Equal(t, contextmeasure.Rate{Tokens: 1, Bytes: 6}, in.Rate)
	verification, err := Verify(t.Context(), cut)
	require.NoError(t, err)
	assert.Equal(t, "verified", verification.Status, "%+v", verification)
	client := &recordedClient{cut: cut, calls: summaryCalls(cut)}
	side, err := Replay(t.Context(), cut, in, client)
	require.NoError(t, err)
	assert.Equal(t, cut.Old.Kind, side.Kind)
	assert.Empty(t, side.Kind)
}

func TestBaselineChunkSettingsTampering(t *testing.T) {
	rec := recordedFixture(t)
	rec.Runs[0].ContextSettings.MaxSummaryChunks = 1
	v, err := Verify(t.Context(), Cuts(rec)[0])
	require.NoError(t, err)
	assert.Equal(t, "mismatch", v.Status)
	assert.Equal(t, 7, v.FirstCall)
	assert.Equal(t, "context_settings", v.Field)
}

func (s *captureStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	h, err := s.Store.Put(ctx, scope, data)
	if err == nil {
		s.content[h] = data
	}
	return h, err
}

// recordedFixture captures the real production summary activity, including exact
// requests, response hashes, checkpoint bytes, sequence and attempt identity.
func recordedFixture(t *testing.T) *runrecord.RunRecord {
	t.Helper()
	return recordedFixtureWith(t, "", nil)
}

// recordedFixtureWith can replace the evicted message with a tool result or
// supply adopted overflow facts before the production summary activity.
func recordedFixtureWith(t *testing.T, result string, overflow *contextmeasure.RequestFacts) *runrecord.RunRecord {
	t.Helper()
	ctx := t.Context()
	store := &captureStore{Store: inmem.New(), content: map[runstore.Hash][]byte{}}
	put := func(v any) runstore.Hash {
		data, err := callrecord.Encode(v)
		require.NoError(t, err)
		h, err := store.Put(ctx, "root", data)
		require.NoError(t, err)
		return h
	}
	user := put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("original fact ", 600)}}})
	tail := put(&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "tail"}}})
	entry := index.MessageEntry{ID: 1, Kind: index.Message, Role: model.ConversationRoleUser, Hash: user, Bytes: 7800}
	if result != "" {
		entry = index.MessageEntry{ID: 1, Kind: index.ToolResult, Batch: 1, Hash: put(result), Bytes: len(result), ToolUseIDs: []string{"result-1"}, Result: &index.ResultProjection{ContentBytes: len(result) + 2, Preview: index.TextParameter{Text: strings.Repeat("evidence ", 600)}}}
	}
	seg, err := index.NewSegment("", "", []index.MessageEntry{entry, {ID: 2, Kind: index.Message, Role: model.ConversationRoleAssistant, Hash: tail, Bytes: 4}})
	require.NoError(t, err)
	data, _, err := index.EncodeSegment(seg)
	require.NoError(t, err)
	head, err := store.Put(ctx, "root", data)
	require.NoError(t, err)
	template, err := store.Put(ctx, "root", []byte("Previous: {{.Previous}}\nEvidence: {{.Evicted}}\n{{.Instructions}} {{.Budget}}"))
	require.NoError(t, err)
	settings := compaction.ContextSettings{Summary: compaction.SummaryConfig{Enabled: true, Model: compaction.ContextModel{ID: "summary"}}, Prompt: compaction.SummaryPrompt{Ref: prompt.PromptRef{ID: "agent.context.summary", Version: "v1"}, Template: template}}
	if result != "" {
		settings.MaxSummarisedResultTokens = 1000
	}
	canonical := compaction.Canonical(settings)
	fingerprint, err := store.Put(ctx, "root", canonical)
	require.NoError(t, err)
	script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("old fact"), agenttest.Usage(10, 5)))
	require.NoError(t, err)
	script.WithContextLimits(model.ContextLimits{ContextWindow: 200000})
	rt := agRuntime.New(agRuntime.WithRunStore(store))
	require.NoError(t, rt.RegisterModel("summary", script.Client()))
	for range 6 {
		_, err := store.NextSeq(ctx, "root", "run")
		require.NoError(t, err)
	}
	view := index.ViewRef{IndexHead: head, TailFrom: 1}
	in := &agRuntime.SummarizeContextInput{AgentID: "agent", RunID: "run", Scope: "root", SessionID: "session", TurnID: "turn", Step: 9, ViewRef: view, From: 1, To: 2, Budget: index.RenderBudget{KeepUserTokens: 150, MaxSummaryTokens: 1000, ReceiptTokens: 200, FramingTokens: 200}, ContextSettings: settings, ContextFingerprint: string(fingerprint), ContextModel: settings.Summary.Model, PromptID: "agent.context.summary"}
	if overflow != nil {
		state, err := compaction.ObserveEffective(compaction.State{}, []contextmeasure.RequestFacts{*overflow}, settings)
		require.NoError(t, err)
		in.Rate = compaction.SummaryRate(state)
	}
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(ctx, 2), in)
	require.NoError(t, err)
	require.Empty(t, out.Kind, "script: %v; output: %+v", script.Err(), out)
	require.NotNil(t, out.Checkpoint)
	var checkpoint index.CheckpointRecord
	require.NoError(t, decode(store.content[*out.Checkpoint], &checkpoint))
	post := view
	post.Checkpoint = out.Checkpoint
	post.TailFrom = 2
	post.Budget = in.Budget
	r := runrecord.Run{RunID: "run", ContextSettings: &settings, ContextFingerprint: string(fingerprint), Entries: seg.Entries, IndexHead: head, Checkpoints: []runrecord.Checkpoint{{Scope: "root", Hash: *out.Checkpoint, Record: checkpoint}}}
	r.Events = append(r.Events, testEvent(t, hooks.PlannerStep, &hooks.PlannerStepEvent{Step: 8, ViewRef: view}), testEvent(t, hooks.ContextCompacted, &hooks.ContextCompactionEvent{Reason: "threshold", Tier: "summary", Step: 9, Checkpoint: out.Checkpoint, ViewRef: &post, Kind: out.Kind, CallKeys: out.CallKeys, Usage: out.Usage, Chunks: out.Chunks, Prompt: out.Prompt}))
	if overflow != nil {
		step := hooks.PlannerStepEvent{Step: 8, ViewRef: view, CallKeys: []runstore.CallKey{overflow.Call}}
		event := hooks.NewContextOverflowEvent(&step, contextmeasure.ContextOverflow{Source: contextmeasure.OverflowProvider, Failed: *overflow}, "recovered", nil)
		r.Events[0] = testEvent(t, hooks.ContextOverflow, event)
		r.Calls = append(r.Calls, runrecord.Call{Record: runstore.CallRecord{Key: overflow.Call}, Class: runrecord.Adopted, Activity: callrecord.ActivityPlanner, Step: 8, Completion: &callrecord.Completion{Facts: overflow}})
	}
	calls, err := store.ListCalls(ctx, "root", "run")
	require.NoError(t, err)
	for _, c := range calls {
		s, err := callrecord.DecodeStarted(c.Started)
		require.NoError(t, err)
		done, err := callrecord.DecodeCompletion(c.Completion)
		require.NoError(t, err)
		req, err := callrecord.Rebuild(ctx, store, "root", s, nil)
		require.NoError(t, err)
		r.Calls = append(r.Calls, runrecord.Call{Record: c, Class: runrecord.Adopted, Activity: s.Activity, Step: s.Step, Started: s, Completion: &done, Request: req})
	}
	return &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{r}, Content: store.content}
}

func putFixture(t *testing.T, rec *runrecord.RunRecord, text string) runstore.Hash {
	t.Helper()
	s := &captureStore{Store: inmem.New(), content: rec.Content}
	h, err := s.Put(t.Context(), rec.RootRunID, []byte(text))
	require.NoError(t, err)
	return h
}

func TestRecordedResponseTextMatchesProduction(t *testing.T) {
	response := &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "one"}, model.TextPart{Text: "two"}}}, {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "three"}}}}}
	assert.Equal(t, "one\ntwo\nthree", responseText(response))
}
