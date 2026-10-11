package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checkpointFaultStore struct {
	runstore.Store
	fail bool
	puts int
}

func TestCompactedReceiptChain(t *testing.T) {
	store := storeinmem.New()
	base := compactionFixture(t, store, 1000)
	rt := New(WithRunStore(store))
	view := base.view()
	view.Budget = compactionBudget()
	var previous *runstore.Hash
	for step := 50; step < 1000; step += 50 {
		view.TailFrom = index.MessageID(2 + 2*step)
		loaded, err := rt.loadView(t.Context(), "root", "run", view)
		require.NoError(t, err)
		require.NotNil(t, loaded.View)
		assertCompactedGrammar(t, loaded)
		view.IndexHead, view.Pending = loaded.Head, nil
		view.Checkpoint = &loaded.View.Checkpoint
		view.Pinned = loaded.View.Pinned
		data, err := store.Get(t.Context(), "root", *view.Checkpoint)
		require.NoError(t, err)
		var record index.CheckpointRecord
		require.NoError(t, json.Unmarshal(data, &record))
		assert.Equal(t, previous, record.Prev)
		assert.Equal(t, view.TailFrom-1, record.Through)
		assert.Equal(t, base.Entries[record.Through-1].Chain, record.Chain)
		previous = new(*view.Checkpoint)
		input, err := checkpoint.FromRecord(t.Context(), func(ctx context.Context, h runstore.Hash) ([]byte, error) {
			return store.Get(ctx, "root", h)
		}, *view.Checkpoint, base.Entries, 0)
		require.NoError(t, err)
		calls, assistants := map[string]bool{}, map[index.MessageID]bool{}
		for item := range input.Receipt {
			if item.ToolUseID != "" {
				assert.False(t, calls[item.ToolUseID])
				calls[item.ToolUseID] = true
				assert.LessOrEqual(t, len(item.Args), checkpoint.ReceiptArgsBytes)
				assert.True(t, utf8.ValidString(item.Args))
			}
			if item.Text != "" {
				assert.False(t, assistants[item.AssistantID])
				assistants[item.AssistantID] = true
			}
		}
		assert.Len(t, calls, step)
		assert.Len(t, assistants, step)
		checkpointText := loaded.Messages[0].Parts[0].(model.TextPart).Text
		receipt := strings.Split(checkpointText, "Behind this checkpoint (framework record):\n")[1]
		assert.LessOrEqual(t, len(receipt), 2*view.Budget.ReceiptTokens)
		collapsed := 0
		for line := range strings.SplitSeq(receipt, "\n") {
			if strings.HasPrefix(line, "…") {
				collapsed++
				assert.Contains(t, line, "[receipt rc-")
			}
		}
		assert.LessOrEqual(t, collapsed, 2)
		assert.NotContains(t, checkpointText, "SECRET")
	}
}

func TestCompactedCutAndGrammar(t *testing.T) {
	store := storeinmem.New()
	base := compactionFixture(t, store, 3)
	rt := New(WithRunStore(store))
	view := base.view()
	view.Budget = compactionBudget()
	view.TailFrom = 3
	loaded, err := rt.loadView(t.Context(), "root", "run", view)
	require.ErrorContains(t, err, "cut")
	assert.Nil(t, loaded)
	view.TailFrom = 4
	loaded, err = rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	assertCompactedGrammar(t, loaded)
}

func TestReceiptPutBeforeReference(t *testing.T) {
	store := &checkpointFaultStore{Store: storeinmem.New()}
	base := compactionFixture(t, store, 3)
	rt := New(WithRunStore(store))
	view := base.view()
	view.Budget = compactionBudget()
	view.TailFrom = 4
	called := 0
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		called++
		return assistantCommitResult(), nil
	}}}
	input := &PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: view, Step: 1}
	store.fail = true
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.ErrorContains(t, err, "Put fault")
	assert.Nil(t, out)
	assert.Zero(t, called)
	store.fail = false
	first, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	require.NotNil(t, first.View)
	second, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	assert.Equal(t, first.View, second.View)
	wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt, hookRuntime: rt}
	require.NoError(t, rt.commitPlanActivityOutput(wf, base, *input, first))
	record, err := runrecord.Read(t.Context(), store, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	require.Len(t, record.Runs[0].Checkpoints, 1)
	assert.Equal(t, first.View.Checkpoint, record.Runs[0].Checkpoints[0].Hash)
}

func TestCheckpointBudgetAndCaches(t *testing.T) {
	store := &measuredViewStore{Store: storeinmem.New()}
	base := compactionFixture(t, store, 3)
	summaryData := []byte("# Long summary\n" + strings.Repeat("summary detail\n", 2300))
	summary, err := store.Put(t.Context(), "root", summaryData)
	require.NoError(t, err)
	old := index.CheckpointRecord{Summary: &summary, SummaryRun: "run", CitedUsers: []index.IndexLine{{ID: 1, Text: "original user"}}, Through: 3, Chain: base.Entries[2].Chain, Calls: []runstore.CallKey{{RunID: "run", Seq: 1, Attempt: 1}}}
	hash := compactionPut(t, store, old)
	view := base.view()
	view.Checkpoint = &hash
	view.Pinned = []index.MessageID{1}
	view.TailFrom = 4
	view.Budget = index.RenderBudget{KeepUserTokens: 400, MaxSummaryTokens: 16000, ReceiptTokens: 240, FramingTokens: 200}
	rt := New(WithRunStore(store))
	first, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	require.NotNil(t, first.View)
	view.IndexHead, view.Pending = first.Head, nil
	view.Pinned = first.View.Pinned
	store.reads.Store(0)
	same, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	assert.Equal(t, first, same)
	assert.Zero(t, store.reads.Load())
	view.Budget = compactionBudget()
	small, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(small.Messages[0].Parts[0].(model.TextPart).Text), checkpoint.MaxBytes(view.Budget))
	assert.Contains(t, small.Messages[0].Parts[0].(model.TextPart).Text, "summary shortened")
	cache := rt.viewCaches["root\x00run"]
	tail := cache.tailMessages
	// Changing effective pins invalidates only the checkpoint message key.
	store.reads.Store(0)
	changedPins, err := cache.checkpointFor(hash, view.Budget, nil)
	require.NoError(t, err)
	assert.Zero(t, store.reads.Load())
	assert.NotEqual(t, small.Messages[0], changedPins)
	assert.Equal(t, tail, cache.tailMessages)
	warm, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	cold, err := New(WithRunStore(store)).loadView(t.Context(), "root", "cold", view)
	require.NoError(t, err)
	assert.Equal(t, warm, cold, "budget shrink and canonical pin selection are cache-independent")

	// Receipt-only successors retain all summary evidence, even under a smaller budget.
	view.TailFrom = 6
	next, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	data, err := store.Get(t.Context(), "root", next.View.Checkpoint)
	require.NoError(t, err)
	var receipt index.CheckpointRecord
	require.NoError(t, json.Unmarshal(data, &receipt))
	assert.Equal(t, old.Summary, receipt.Summary)
	assert.Equal(t, old.SummaryRun, receipt.SummaryRun)
	assert.Equal(t, old.CitedUsers, receipt.CitedUsers)
	assert.Empty(t, receipt.Calls)
}

func TestClearedView(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprint(keep), func(t *testing.T) {
			store := &resultReadStore{Store: storeinmem.New()}
			base := compactionFixture(t, store, 2)
			rt := New(WithRunStore(store))
			entry := &base.Entries[4]
			entry.Keep = keep
			if !keep {
				entry.Result.Error = &index.TextParameter{Text: "first error\nprivate second line"}
				entry.Hash = compactionPutRaw(t, store, "")
			}
			store.result = entry.Hash
			// Changing a fixture entry requires recomputing its chain, never rewriting a stored index.
			for i := 4; i < len(base.Entries); i++ {
				chain, err := index.ComputeChain(base.Entries[i-1].Chain, base.Entries[i])
				require.NoError(t, err)
				base.Entries[i].Chain = chain
			}
			view := base.view()
			view.Budget = compactionBudget()
			view.TailFrom = 4
			view.ClearedThrough = 5
			loaded, err := rt.loadView(t.Context(), "root", "run", view)
			require.NoError(t, err)
			assertCompactedGrammar(t, loaded)
			result := loaded.Messages[2].Parts[0].(model.ToolResultPart)
			assert.Equal(t, "call-1", result.ToolUseID)
			assert.Equal(t, !keep, result.IsError)
			if keep {
				assert.Positive(t, store.reads)
			} else {
				assert.Zero(t, store.reads, "cleared tail renderer never loads result bodies")
			}
			if keep {
				assert.NotContains(t, fmt.Sprint(result.Content), "cleared")
			} else {
				assert.Equal(t, checkpoint.ClearedResult(24, "call-1", "first error"), result.Content)
			}
			again, err := rt.loadView(t.Context(), "root", "run", view)
			require.NoError(t, err)
			assert.Equal(t, canonicalIndexMessages(t, loaded.Messages), canonicalIndexMessages(t, again.Messages))
		})
	}
}

func TestReceiptTextAndPrivacy(t *testing.T) {
	store := storeinmem.New()
	base := compactionFixture(t, store, 1)
	require.NoError(t, base.structural(index.HideBookkeeping, []string{"call-0"}))
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Assistant, Hash: compactionPut(t, store, []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "text only\nsecond"}}}}), Role: model.ConversationRoleAssistant, Bytes: 16}))
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Clarification, Hash: compactionPutRaw(t, store, "next user"), Role: model.ConversationRoleUser, Bytes: 9}))
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Assistant, Hash: compactionPut(t, store, []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "tail answer"}}}}), Role: model.ConversationRoleAssistant, Bytes: 11}))
	view := base.view()
	view.TailFrom = 7
	view.Budget = compactionBudget()
	rt := New(WithRunStore(store))
	loaded, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	assertCompactedGrammar(t, loaded)
	data, err := store.Get(t.Context(), "root", loaded.View.Checkpoint)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "call-0")
	assert.NotContains(t, string(data), "SECRET")
	assert.Contains(t, loaded.Messages[0].Parts[0].(model.TextPart).Text, "text only")
}

func compactionPutRaw(t *testing.T, store runstore.Store, text string) runstore.Hash {
	t.Helper()
	hash, err := store.Put(t.Context(), "root", []byte(text))
	require.NoError(t, err)
	return hash
}

func TestViewUpdateAdoption(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(fmt.Sprint(recovery), func(t *testing.T) {
			rt := New()
			base := &workflowPlanInput{protocolVersion: api.WorkflowProtocolVersion}
			input := PlanActivityInput{AgentID: "agent", RunID: "run", Step: 1, View: index.ViewRef{TailFrom: 4, Budget: compactionBudget()}}
			update := &api.ViewUpdate{Checkpoint: "new-checkpoint", Pinned: []index.MessageID{1}, CheckpointBytes: 123}
			out := &PlanActivityOutput{View: update, Result: assistantCommitResult()}
			if recovery {
				out.Result = nil
				out.Recovery = &ModelRecovery{}
			}
			wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt, hookRuntime: rt}
			require.NoError(t, rt.commitPlanActivityOutput(wf, base, input, out))
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			require.Len(t, page.Events, 1)
			var event hooks.PlannerStepEvent
			require.NoError(t, json.Unmarshal(page.Events[0].Payload, &event))
			if recovery {
				assert.Nil(t, event.ViewRef.Checkpoint)
			} else {
				assert.Equal(t, &update.Checkpoint, event.ViewRef.Checkpoint)
				assert.Equal(t, update.Pinned, event.ViewRef.Pinned)
				assert.Equal(t, 123, base.checkpointBytes)
			}

			if recovery {
				assert.Nil(t, base.view().Checkpoint)
			} else {
				assert.Equal(t, &update.Checkpoint, base.view().Checkpoint)
				assert.Equal(t, update.Pinned, base.view().Pinned)
			}
		})
	}
}

func TestTailInheritsHiddenCalls(t *testing.T) {
	store := storeinmem.New()
	original := compactionFixture(t, store, 2)
	base := &workflowPlanInput{RunContext: original.RunContext}
	for _, entry := range original.Entries[:3] {
		require.NoError(t, base.appendEntry(entry))
	}
	require.NoError(t, base.structural(index.HideBookkeeping, []string{"call-0"}))
	require.NoError(t, base.appendEntry(original.Entries[3]))
	require.NoError(t, base.appendEntry(original.Entries[4]))
	hiddenResult := original.Entries[2]
	hiddenResult.Batch = base.Entries[5].Batch
	require.NoError(t, base.appendEntry(hiddenResult))
	rt := New(WithRunStore(store))
	view := base.view()
	view.TailFrom = 5
	view.Budget = compactionBudget()
	loaded, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	assertCompactedGrammar(t, loaded)
	require.Len(t, loaded.Messages[2].Parts, 1)
	assert.Equal(t, "call-1", loaded.Messages[2].Parts[0].(model.ToolResultPart).ToolUseID)
	// Changing only the clear watermark rebuilds the tail, not the checkpoint.
	cache := rt.viewCaches["root\x00run"]
	message := cache.checkpointMessage
	firstKey := cache.tailKey
	view.Checkpoint = &loaded.View.Checkpoint
	view.Pinned = loaded.View.Pinned
	view.IndexHead = loaded.Head
	view.Pending = nil
	view.ClearedThrough = 6
	next, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	assert.Same(t, message, cache.checkpointMessage)
	assert.NotEqual(t, firstKey, cache.tailKey)
	assertCompactedGrammar(t, next)
	// A new head invalidates the tail while retaining the checkpoint.
	base.adoptHead(loaded.Head)
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Assistant, Hash: compactionPut(t, store, []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "more"}}}}), Role: model.ConversationRoleAssistant, Bytes: 4}))
	view.Pending = base.view().Pending
	oldKey := cache.tailKey
	next, err = rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	assert.NotEqual(t, oldKey, cache.tailKey)
	assert.Same(t, message, cache.checkpointMessage)
	assertCompactedGrammar(t, next)
}

func TestCompactedGrammarRejectsInvalidTail(t *testing.T) {
	for _, kind := range []string{"unanswered", "adjacent"} {
		t.Run(kind, func(t *testing.T) {
			store := storeinmem.New()
			base := compactionFixture(t, store, 2)
			if kind == "unanswered" {
				base.Entries = base.Entries[:4]
			} else {
				require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Assistant, Hash: compactionPut(t, store, []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "one"}}}}), Role: model.ConversationRoleAssistant}))
				require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Assistant, Hash: compactionPut(t, store, []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "two"}}}}), Role: model.ConversationRoleAssistant}))
			}
			rt := New(WithRunStore(store))
			view := base.view()
			view.TailFrom = 4
			view.Budget = compactionBudget()
			loaded, err := rt.loadView(t.Context(), "root", "run", view)
			require.ErrorContains(t, err, "grammar")
			assert.Nil(t, loaded)
		})
	}
}

func TestCompactedActivityRecoveryHasNoViewUpdate(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprint(resume), func(t *testing.T) {
			rt := New()
			base := compactionFixture(t, rt.RunStore, 2)
			rt.models["default"] = &activityRecoveryUsageClient{rejected: restoredValidationError(t, model.OutputValidationToolArguments)}
			registerContentAgent(t, rt, activityRecoveryUsagePlanner{})
			view := base.view()
			view.TailFrom = 4
			view.Budget = compactionBudget()
			out, err := executeContentPlannerActivity(t, rt, &api.PlanActivityInput{AgentID: "content.agent", RunID: "run", RunContext: run.Context{RunID: "run", RootRunID: "root", Attempt: 1}, View: view}, resume)
			require.NoError(t, err)
			require.NotNil(t, out.Recovery)
			assert.Nil(t, out.View)
		})
	}
}

func TestClearedCanonicalSize(t *testing.T) {
	store := &resultReadStore{Store: storeinmem.New()}
	base := compactionFixture(t, store, 2)
	entry := &base.Entries[4]
	entry.Bytes = 5
	entry.Result.ContentBytes = 100002
	entry.Result.Preview = index.TextParameter{Text: "short"}
	entry.Hash = compactionPut(t, store, strings.Repeat("x", 100000))
	store.result = entry.Hash
	chain, err := index.ComputeChain(base.Entries[3].Chain, *entry)
	require.NoError(t, err)
	entry.Chain = chain
	view := base.view()
	view.TailFrom = 4
	view.ClearedThrough = 5
	view.Budget = compactionBudget()
	loaded, err := New(WithRunStore(store)).loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	assert.Equal(t, checkpoint.ClearedResult(100002, "call-1", ""), loaded.Messages[2].Parts[0].(model.ToolResultPart).Content)
	assert.Zero(t, store.reads)
}

func (s *checkpointFaultStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	var record map[string]any
	if err := json.Unmarshal(data, &record); err == nil && record["through"] != nil && record["receipt"] != nil {
		s.puts++
		if s.fail {
			return "", errors.New("checkpoint Put fault")
		}
	}
	return s.Store.Put(ctx, scope, data)
}

func compactionFixture(t *testing.T, store runstore.Store, steps int) *workflowPlanInput {
	t.Helper()
	base := &workflowPlanInput{AgentID: "agent", RunContext: run.Context{RootRunID: "root", RunID: "run"}, protocolVersion: api.WorkflowProtocolVersion}
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Hash: compactionPut(t, store, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "original user"}}}), Role: model.ConversationRoleUser, Bytes: 13}))
	for i := range steps {
		id := fmt.Sprintf("call-%d", i)
		call := compactionPut(t, store, map[string]any{"Name": "test.lookup", "ToolCallID": id, "Payload": rawjson.Message(`{"query":"` + strings.Repeat("é", 100) + `"}`), "ServerData": "SERVER_SECRET", "Injected": "INJECTED_SECRET"})
		text := compactionPut(t, store, []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: fmt.Sprintf("assistant %d\nsecond line", i)}}}})
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Assistant, Hash: text, Calls: []runstore.Hash{call}, ToolUseIDs: []string{id}, Role: model.ConversationRoleAssistant, Bytes: 230}))
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.ToolResult, Hash: compactionPut(t, store, rawjson.Message(`{"value":"whole result"}`)), ToolUseIDs: []string{id}, Batch: index.MessageID(len(base.Entries) + 1), Role: model.ConversationRoleUser, Bytes: 24, Result: &index.ResultProjection{ContentBytes: 24}}))
	}
	return base
}

func compactionPut(t *testing.T, store runstore.Store, value any) runstore.Hash {
	t.Helper()
	data, err := json.Marshal(value, json.Deterministic(true))
	require.NoError(t, err)
	hash, err := store.Put(t.Context(), "root", data)
	require.NoError(t, err)
	return hash
}

func compactionBudget() index.RenderBudget {
	return index.RenderBudget{KeepUserTokens: 400, MaxSummaryTokens: 800, ReceiptTokens: 240, FramingTokens: 200}
}

func assertCompactedGrammar(t *testing.T, loaded *loadedView) {
	t.Helper()
	assert.NoError(t, transcript.ValidateView(loaded.Messages, transcript.GrammarAnthropic{}))
	assert.NoError(t, transcript.ValidateView(loaded.Messages, transcript.GrammarBedrock{}))
}

func TestPrefixHiddenConsumesStructuralResults(t *testing.T) {
	for _, kind := range []index.StructuralKind{index.UnknownTool, index.PolicyDenial, index.ConfirmationDenial, index.SynthesizedError, index.Cancellation} {
		t.Run(string(kind), func(t *testing.T) {
			entries := []index.MessageEntry{
				{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.HideBookkeeping}, ToolUseIDs: []string{"consumed", "pending"}},
				{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: kind}, ToolUseIDs: []string{"consumed"}},
			}
			assert.Equal(t, map[string]bool{"pending": true}, prefixHidden(entries))
		})
	}
}
