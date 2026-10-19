package statefulevaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type measuredContext struct {
	TextBytes     int
	FixedBytes    int
	ViewTextBytes int
	NonText       int
	ViewNonText   int
	ViewBearing   bool
	ContextWindow int
	OutputReserve int
}

type pressureResult struct {
	Arm                     string
	Phase                   string
	InputBudget             int
	Before                  measuredContext
	After                   measuredContext
	BeforeEstimatedTokens   int
	AfterEstimatedTokens    int
	EstimateAdmissionBefore string
	EstimateAdmissionAfter  string
	Decision                compaction.Decision
	RetainedNames           []string
	RetainedBytes           int
	Invocation              *invocation
}

func TestCompactionPreservesDiscoveredSchemaOverhead(t *testing.T) {
	for _, row := range runPressure(t) {
		assert.Equal(t, compaction.Clear, row.Decision.Tier)
		assert.Less(t, row.After.ViewTextBytes, row.Before.ViewTextBytes)
		assert.Equal(t, row.Before.FixedBytes, row.After.FixedBytes)
		assert.Positive(t, row.After.FixedBytes)
		assert.Equal(t, "reject", row.EstimateAdmissionBefore)
		if row.Arm == "native" && row.Phase != "initial" {
			assert.Contains(t, row.RetainedNames, "summarize_text")
		}
	}
}

// runPressure applies the public compaction decision and renderer to equal history.
// Admission is only the declared byte estimator, never a provider observation.
func runPressure(t *testing.T) []pressureResult {
	t.Helper()
	entries, source := pressureHistory(t)
	original, err := checkpoint.Project(t.Context(), source, entries, index.ViewRef{})
	require.NoError(t, err)
	require.NoError(t, transcript.ValidateView(original, transcript.GrammarCanonical{}))
	rows := make([]pressureResult, 0, 12)
	for _, arm := range []string{"compact", "native"} {
		f := newEvaluationFixture(t)
		key := discoveryKey{Agent: "a", Session: "pressure"}
		for _, phase := range []string{"initial", "discovered", "repeat"} {
			var call *invocation
			if phase == "discovered" {
				require.Contains(t, f.discover(t, key, "summarize_text").Names, "summarize_text")
				if arm == "compact" {
					f.state = newDiscoveryState()
				}
			}
			if phase == "repeat" {
				result := f.invoke(t, arm, key, "summarize_text", taskArguments(t, "summarize_text"))
				require.Empty(t, result.Failure)
				call = &result
			}
			beforeState, err := json.Marshal(f.state.current(key, f.generation))
			require.NoError(t, err)
			for _, budget := range []int{3000, 6000} {
				req := &model.Request{Model: "estimated-local", MaxTokens: 1000, Messages: original, Tools: modelTools(t, f.registration(arm, key).Specs)}
				limits := model.ContextLimits{ContextWindow: budget + 1000}
				before, err := contextmeasure.Measure(req, limits, original)
				require.NoError(t, err)
				state := compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{before})
				decision := compaction.Decide(state, entries, index.ViewRef{}, compaction.Settings{MaxTier: compaction.Clear, TriggerAt: contextmeasure.Rate{Tokens: 4, Bytes: 5}, ProtectRecentToolTokens: 512, MinSavingTokens: 100})
				projected, err := checkpoint.Project(t.Context(), source, entries, decision.View)
				require.NoError(t, err)
				require.NoError(t, transcript.ValidateView(projected, transcript.GrammarCanonical{}))
				req.Messages = projected
				after, err := contextmeasure.Measure(req, limits, projected)
				require.NoError(t, err)
				beforeTokens := contextmeasure.Estimate(contextmeasure.Rate{Tokens: 1, Bytes: 3}, before.FixedBytes, before.ViewTextBytes, before.NonText)
				afterTokens := contextmeasure.Estimate(contextmeasure.Rate{Tokens: 1, Bytes: 3}, after.FixedBytes, after.ViewTextBytes, after.NonText)
				rows = append(rows, pressureResult{Arm: arm, Phase: phase, InputBudget: budget, Before: measuredFacts(before), After: measuredFacts(after), BeforeEstimatedTokens: beforeTokens, AfterEstimatedTokens: afterTokens, EstimateAdmissionBefore: estimatedAdmission(beforeTokens, budget), EstimateAdmissionAfter: estimatedAdmission(afterTokens, budget), Decision: decision, RetainedNames: f.state.names(key, f.generation), RetainedBytes: f.state.bytes(key, f.generation), Invocation: call})
			}
			afterState, err := json.Marshal(f.state.current(key, f.generation))
			require.NoError(t, err)
			assert.Equal(t, beforeState, afterState, "compaction must not mutate discovery state")
		}
	}
	return rows
}

func estimatedAdmission(tokens, budget int) string {
	if tokens > budget {
		return "reject"
	}
	return "accept"
}

// pressureHistory stores actual model messages/results with measured index metadata.
func pressureHistory(t *testing.T) ([]index.MessageEntry, index.Source) {
	t.Helper()
	blobs := make(map[runstore.Hash][]byte)
	store := func(value any) runstore.Hash {
		data, err := json.Marshal(value, json.Deterministic(true))
		require.NoError(t, err)
		hash := runstore.Hash(fmt.Sprintf("%x", sha256.Sum256(data)))
		blobs[hash] = data
		return hash
	}
	leading := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "historical task"}}}
	entries := make([]index.MessageEntry, 0, 18)
	entries = append(entries, index.MessageEntry{ID: 1, Kind: index.Message, Hash: store(leading), Role: leading.Role, Bytes: len("historical task")})
	for i := range 8 {
		id := fmt.Sprintf("history-%d", i)
		assistant := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: id, Name: "summarize_text", Input: map[string]any{"text": "historical"}}}}
		size, err := contextmeasure.MeasureMessages([]*model.Message{assistant})
		require.NoError(t, err)
		entries = append(entries, index.MessageEntry{ID: index.MessageID(len(entries) + 1), Kind: index.Message, Hash: store(assistant), Role: assistant.Role, Bytes: size.Bytes, ToolUseIDs: []string{id}})
		content := strings.Repeat("x", 4096)
		result := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: id, Content: content}}}
		size, err = contextmeasure.MeasureMessages([]*model.Message{result})
		require.NoError(t, err)
		entryID := index.MessageID(len(entries) + 1)
		entries = append(entries, index.MessageEntry{ID: entryID, Kind: index.ToolResult, Hash: store(content), Role: result.Role, Bytes: size.Bytes, ToolUseIDs: []string{id}, Batch: entryID, Result: &index.ResultProjection{ContentBytes: len(content) + 2}})
	}
	user := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("u", 128)}}}
	entries = append(entries, index.MessageEntry{ID: index.MessageID(len(entries) + 1), Kind: index.Message, Hash: store(user), Role: user.Role, Bytes: 128})
	return entries, func(_ context.Context, hash runstore.Hash) ([]byte, error) {
		data, ok := blobs[hash]
		if !ok {
			return nil, fmt.Errorf("missing pressure blob %s", hash)
		}
		return bytes.Clone(data), nil
	}
}

// measuredFacts preserves measured scalars without serializing unrelated duration facts.
func measuredFacts(f contextmeasure.RequestFacts) measuredContext {
	return measuredContext{TextBytes: f.TextBytes, FixedBytes: f.FixedBytes, ViewTextBytes: f.ViewTextBytes, NonText: f.NonText, ViewNonText: f.ViewNonText, ViewBearing: f.ViewBearing, ContextWindow: f.ContextWindow, OutputReserve: f.OutputReserve}
}
