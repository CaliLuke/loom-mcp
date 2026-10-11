package recall_test

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storemem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessionmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scriptFixture struct {
	t     *testing.T
	h     recall.Handler
	req   recall.Request
	store runstore.Store
}

type evidenceAgent struct {
	t                *testing.T
	fixture          *scriptFixture
	visible          string
	window           int
	allowance        compaction.RecallAllowances
	reads, discovery int
	recovered        map[string]string
}

// TestRecallScript1000Steps gives the controller only rendered text and lookup
// responses. The oracle is held by the test, never passed to the controller.
func TestRecallScript1000Steps(t *testing.T) {
	logs := map[string]map[string]int{}
	for _, window := range []int{200_000, 8_000} {
		t.Run(strconv.Itoa(window), func(t *testing.T) {
			f, entries, oracle := scriptHistory(t)
			state := compaction.State{MinB: window, Models: map[string]compaction.ModelState{"script": {Rate: contextmeasure.Rate{Tokens: 1, Bytes: 4}, Budget: window, Known: true}}}
			decision := compaction.Decide(state, entries, f.req.View, compaction.Settings{})
			require.Greater(t, decision.View.TailFrom, index.MessageID(1))
			through := decision.View.TailFrom - 1
			var previous *runstore.Hash
			// Three committed Prev records prove discovery across repeated compactions.
			for part := 1; part <= 3; part++ {
				end := through * index.MessageID(part) / 3
				begin := through*index.MessageID(part-1)/3 + 1
				record := index.CheckpointRecord{Through: end, Chain: entries[end-1].Chain, Prev: previous, Receipt: scriptReceipt(t, f, entries[begin-1:end])}
				hash := f.put(record)
				previous = &hash
			}
			f.req.View = decision.View
			f.req.View.Checkpoint = previous
			f.req.PageBytes = compaction.Settings{}.RecallPageBytes(window)
			source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
				return f.store.Get(ctx, f.req.Scope, hash)
			}
			view, err := checkpoint.Project(t.Context(), source, entries, f.req.View)
			require.NoError(t, err)
			var visible strings.Builder
			for _, message := range view {
				for _, part := range message.Parts {
					if text, ok := part.(model.TextPart); ok {
						visible.WriteString(text.Text)
					}
					if result, ok := part.(model.ToolResultPart); ok {
						data, err := json.Marshal(result.Content)
						require.NoError(t, err)
						visible.Write(data)
					}
				}
			}
			assert.NotContains(t, visible.String(), "recall_context")
			controller := evidenceAgent{t: t, fixture: f, visible: visible.String(), window: window, allowance: compaction.NewRecall(compaction.Settings{RecallReadCalls: 1_000_000, RecallDiscoveryCalls: 1_000_000}), recovered: map[string]string{}}
			controller.recover()
			wanted := 0
			for key, value := range oracle {
				fields := strings.SplitN(key, "/", 2)
				messageID, err := strconv.ParseUint(fields[0], 10, 64)
				require.NoError(t, err)
				if index.MessageID(messageID) > through {
					continue
				}
				wanted++
				assert.Equal(t, value, controller.recovered[fields[1]], "original evidence at %s", key)
			}
			assert.Positive(t, wanted)
			logs[strconv.Itoa(window)] = map[string]int{"steps": 1000, "evicted_evidence": wanted, "read_calls": controller.reads, "discovery_calls": controller.discovery}
			t.Logf("P5 window=%d evicted=%d reads=%d discovery=%d", window, wanted, controller.reads, controller.discovery)
		})
	}
	data, err := json.Marshal(logs, json.Deterministic(true))
	require.NoError(t, err)
	path := "testdata/recall_script_calls.json"
	if os.Getenv("LOOM_MCP_RECORD_RECALL_CALLS") == "1" {
		require.NoError(t, os.MkdirAll("testdata", 0750))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
	}
	recorded, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.JSONEq(t, string(recorded), string(data))
}

func scriptHistory(t *testing.T) (*scriptFixture, []index.MessageEntry, map[string]string) {
	t.Helper()
	sessions := sessionmem.New()
	_, err := sessions.CreateSession(t.Context(), "session", time.Unix(1, 0))
	require.NoError(t, err)
	require.NoError(t, sessions.UpsertRun(t.Context(), session.RunMeta{RunID: "run", SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning, StartedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}))
	store := storemem.New()
	f := &scriptFixture{t: t, store: store, h: recall.Handler{Store: store, Sessions: sessions}, req: recall.Request{RunID: "run", Scope: "root", PageBytes: 512, ScanBytes: 8_000_000}}
	var entries []index.MessageEntry
	oracle := map[string]string{}
	add := func(entry index.MessageEntry, id, part, text string) {
		entry.ID = index.MessageID(len(entries) + 1)
		entries = append(entries, entry)
		oracle[fmt.Sprintf("%d/%s/%s", entry.ID, id, part)] = text
	}
	for step := 0; step < 1000; step++ {
		id := strconv.Itoa(len(entries) + 1)
		kind := step % 7
		switch kind {
		case 0, 1, 2, 6:
			text := fmt.Sprintf("original step %d ", step) + strings.Repeat("evidence ", 550)
			role := model.ConversationRoleUser
			entryKind := index.Message
			var stored any = &model.Message{Role: role, Parts: []model.Part{model.TextPart{Text: text}}}
			if kind == 1 || kind == 6 {
				role = model.ConversationRoleAssistant
				entryKind = index.Assistant
				stored = []*model.Message{{Role: role, Parts: []model.Part{model.TextPart{Text: text}}}}
			}
			add(index.MessageEntry{Kind: entryKind, Role: role, Hash: f.put(stored), Bytes: len(text)}, id, "result", text)
		case 3:
			a, b := fmt.Sprintf("toolu_%d_A", step), fmt.Sprintf("toolu_%d_B", step)
			payloadA, payloadB := fmt.Sprintf(`{"query":"original A %d"}`, step), fmt.Sprintf(`{"query":"original B %d"}`, step)
			hashes := []runstore.Hash{f.put(map[string]any{"Name": "svc.search", "ToolCallID": a, "Payload": rawjson.Message(payloadA)}), f.put(map[string]any{"Name": "svc.search", "ToolCallID": b, "Payload": rawjson.Message(payloadB)})}
			add(index.MessageEntry{Kind: index.Assistant, Role: model.ConversationRoleAssistant, Calls: hashes, ToolUseIDs: []string{a, b}, Bytes: len(payloadA) + len(payloadB)}, a, "arguments", payloadA)
			oracle[fmt.Sprintf("%d/%s/arguments", len(entries), b)] = payloadB
		case 4, 5:
			batch := index.MessageID(step+5) - index.MessageID(step%7)
			suffix := "B"
			if kind == 5 {
				suffix = "A"
			}
			callID := fmt.Sprintf("toolu_%d_%s", step-step%7+3, suffix)
			text := fmt.Sprintf(`"original result %s"`, callID)
			add(index.MessageEntry{Kind: index.ToolResult, Role: model.ConversationRoleUser, Hash: f.raw([]byte(text)), ToolUseIDs: []string{callID}, Batch: batch, Bytes: len(text), Result: &index.ResultProjection{ContentBytes: len(text)}}, callID, "result", text)
		}
	}
	segment, err := index.NewSegment("", "", entries)
	require.NoError(t, err)
	data, _, err := index.EncodeSegment(segment)
	require.NoError(t, err)
	f.req.Head = f.raw(data)
	f.req.View.IndexHead = f.req.Head
	loaded, err := index.Walk(t.Context(), func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return f.store.Get(ctx, f.req.Scope, hash)
	}, f.req.Head)
	require.NoError(t, err)
	return f, loaded, oracle
}

func scriptReceipt(t *testing.T, f *scriptFixture, entries []index.MessageEntry) []index.ReceiptLine {
	t.Helper()
	var lines []index.ReceiptLine
	for _, entry := range entries {
		if entry.Kind == index.Assistant && entry.Hash != "" {
			lines = append(lines, index.ReceiptLine{Kind: "assistant", From: entry.ID, Through: entry.ID, Text: "original assistant line"})
		}
		for i, hash := range entry.Calls {
			data, err := f.store.Get(t.Context(), f.req.Scope, hash)
			require.NoError(t, err)
			var call struct{ Payload rawjson.Message }
			require.NoError(t, json.Unmarshal(data, &call))
			lines = append(lines, index.ReceiptLine{Kind: "call", From: entry.ID, Through: entry.ID, Tool: "svc.search", ToolUseID: entry.ToolUseIDs[i], Text: string(call.Payload)})
		}
	}
	return lines
}

func (a *evidenceAgent) invoke(input recall.Input) recall.Response {
	a.t.Helper()
	data, err := json.Marshal(input)
	require.NoError(a.t, err)
	script, err := agenttest.NewScript(agenttest.Respond(agenttest.RawToolCall(tools.RecallContext, rawjson.Message(data)), agenttest.CallID(fmt.Sprintf("lookup-%d", a.reads+a.discovery))))
	require.NoError(a.t, err)
	script.WithContextLimits(model.ContextLimits{ContextWindow: a.window})
	var schema any
	require.NoError(a.t, json.Unmarshal(recall.Spec().Payload.Schema, &schema))
	response, err := script.Client().Complete(a.t.Context(), &model.Request{Model: "script", Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: a.visible}}}}, Tools: []*model.ToolDefinition{{Name: tools.RecallContext.String(), Description: recall.Description, InputSchema: schema}}})
	require.NoError(a.t, err)
	require.NoError(a.t, script.Err())
	require.Len(a.t, response.ToolCalls, 1)
	parsed, err := recall.ParseInput(response.ToolCalls[0].Payload)
	require.NoError(a.t, err)
	require.True(a.t, a.allowance.Charge(parsed.Kind == recall.Read))
	if parsed.Kind == recall.Read {
		a.reads++
	} else {
		a.discovery++
	}
	result, err := a.fixture.h.Handle(a.t.Context(), a.fixture.req, parsed)
	require.NoError(a.t, err)
	require.Empty(a.t, result.Code)
	return result
}

func (a *evidenceAgent) recover() {
	a.t.Helper()
	header := regexp.MustCompile(`Messages 1–([0-9]+) not shown`).FindStringSubmatch(a.visible)
	require.Len(a.t, header, 2)
	through, err := strconv.ParseUint(header[1], 10, 64)
	require.NoError(a.t, err)
	// Only the visible range and descriptors returned by lookup supply read IDs.
	selector := recall.ListInput{From: 1, Through: index.MessageID(through)}
	for {
		response := a.invoke(recall.Input{Kind: recall.List, List: &selector})
		for _, line := range response.Entries {
			parts := []recall.Part{recall.Result}
			if line.Kind == "call" {
				parts = []recall.Part{recall.Arguments, recall.Result}
			}
			for _, part := range parts {
				key := line.ID + "/" + string(part)
				if _, read := a.recovered[key]; read {
					continue
				}
				request := recall.ReadInput{ID: line.ID, Part: part, Offset: 0}
				var text strings.Builder
				for {
					page := a.invoke(recall.Input{Kind: recall.Read, Read: &request})
					text.WriteString(page.Text)
					if page.NextOffset == nil {
						break
					}
					request.Offset = *page.NextOffset
				}
				a.recovered[key] = text.String()
			}
		}
		if response.Cursor == "" {
			break
		}
		selector.Cursor = response.Cursor
	}
}

func (f *scriptFixture) put(value any) runstore.Hash {
	f.t.Helper()
	data, err := json.Marshal(value)
	require.NoError(f.t, err)
	return f.raw(data)
}

func (f *scriptFixture) raw(data []byte) runstore.Hash {
	f.t.Helper()
	hash, err := f.store.Put(f.t.Context(), f.req.Scope, data)
	require.NoError(f.t, err)
	return hash
}
