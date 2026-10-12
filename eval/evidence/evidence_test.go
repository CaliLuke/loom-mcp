package evidence

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func msg(text string) *model.Message {
	return &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: text}}}
}
func call(seq int, messages ...*model.Message) runrecord.Call {
	return runrecord.Call{Class: runrecord.Adopted, Activity: callrecord.ActivityPlanner, Started: callrecord.Started{Key: runstore.CallKey{Seq: seq, Attempt: 1}}, Request: &model.Request{Messages: messages}}
}
func spec() Spec {
	return Spec{Needs: []Need{{Trigger: "NEED:", Evidence: []EvidenceID{"fact"}}}, Evidence: map[EvidenceID]Evidence{"fact": {Value: "ABC-12345"}}, Answer: []string{"ABC-12345"}, Grammar: regexp.MustCompile(`[A-Z]{3}-[0-9A-Z]{5}`)}
}
func record(calls ...runrecord.Call) *runrecord.RunRecord {
	return &runrecord.RunRecord{RootRunID: "r1", Runs: []runrecord.Run{{RunID: "r1", Calls: calls}}, Content: map[runstore.Hash][]byte{"fact": []byte("ABC-12345")}}
}
func TestSummaryRetainedAndRecall(t *testing.T) {
	cp, err := checkpoint.Render(checkpoint.Input{Through: 3, ID: "cp", Summary: &checkpoint.Summary{Text: "ABC-12345"}}, index.RenderBudget{KeepUserTokens: 100, MaxSummaryTokens: 100, ReceiptTokens: 100, FramingTokens: 300}, checkpoint.Limits{}, nil)
	require.NoError(t, err)
	r := record(call(1, cp, msg("NEED:")))
	r.Runs[0].Transcript = []*model.Message{answer("ABC-12345")}
	got, err := Classify([]*runrecord.RunRecord{r}, spec(), true)
	require.NoError(t, err)
	assert.True(t, got.Used)
	assert.False(t, got.RecallQualifying)
	assert.Contains(t, got.Sources, Summary)
	r.Runs[0].Calls = []runrecord.Call{call(1, msg("NEED:")), call(2, msg("NEED:"), recallResult("ABC-12345"))}
	got, err = Classify([]*runrecord.RunRecord{r}, spec(), true)
	require.NoError(t, err)
	assert.True(t, got.RecallSuccess)
	r.Runs[0].Calls = r.Runs[0].Calls[:1]
	got, err = Classify([]*runrecord.RunRecord{r}, spec(), true)
	require.NoError(t, err)
	assert.True(t, got.RecallQualifying)
	assert.False(t, got.RecallSuccess)
}
func answer(s string) *model.Message {
	m := msg(s)
	m.Role = model.ConversationRoleAssistant
	return m
}
func recallResult(s string) *model.Message {
	return &model.Message{Parts: []model.Part{model.ToolUsePart{ID: "recall", Name: "runtime.recall_context"}, model.ToolResultPart{ToolUseID: "recall", Content: s}}}
}
func TestNeedAdoptionAndUnsupported(t *testing.T) {
	abandoned := call(1, msg("NEED: ABC-12345"))
	abandoned.Class = runrecord.Abandoned
	r := record(abandoned, call(2, msg("hello")))
	r.Runs[0].Transcript = []*model.Message{answer("ABC-12345")}
	got, err := Classify([]*runrecord.RunRecord{r}, spec(), true)
	require.NoError(t, err)
	assert.False(t, got.NeedFound)
	assert.False(t, got.Used)
	assert.False(t, got.RecallQualifying)
	r.Runs[0].Calls = append(r.Runs[0].Calls, call(3, msg("NEED: ABC-12345")))
	r.Runs[0].Transcript = []*model.Message{answer("ABC-12345 XYZ-00000")}
	got, err = Classify([]*runrecord.RunRecord{r}, spec(), true)
	require.NoError(t, err)
	assert.True(t, got.UnsupportedClaim)
	r.Runs[0].Calls = append(r.Runs[0].Calls, call(4, msg("XYZ-00000")))
	got, err = Classify([]*runrecord.RunRecord{r}, spec(), true)
	require.NoError(t, err)
	assert.False(t, got.UnsupportedClaim)
	s := spec()
	s.Decoys = []string{"XYZ-00000"}
	got, err = Classify([]*runrecord.RunRecord{r}, s, true)
	require.NoError(t, err)
	assert.True(t, got.UnsupportedClaim)
}
func TestAssets(t *testing.T) {
	for _, part := range []model.Part{model.ImagePart{Bytes: []byte("asset")}, model.DocumentPart{Bytes: []byte("asset")}} {
		s := spec()
		s.Evidence["fact"] = Evidence{Asset: fmt.Sprintf("%x", sha256.Sum256([]byte("asset"))), Token: "ABC-12345"}
		m := msg("NEED:")
		m.Parts = append(m.Parts, part)
		r := record(call(1, m))
		r.Runs[0].Transcript = []*model.Message{answer("ABC-12345")}
		got, err := Classify([]*runrecord.RunRecord{r}, s, true)
		require.NoError(t, err)
		assert.False(t, got.AbsentWhenNeeded)
		assert.False(t, got.UnsupportedClaim, "%T supports its rendered token", part)
		r.Runs[0].Calls = []runrecord.Call{call(1, msg("NEED:")), call(2, recallResult("recall_unsupported_part"))}
		got, err = Classify([]*runrecord.RunRecord{r}, s, true)
		require.NoError(t, err)
		assert.True(t, got.AbsentWhenNeeded)
		assert.True(t, got.UnsupportedClaim, "an asset digest alone cannot support its token")
		assert.False(t, got.RecallSuccess)
	}
}

func TestAllSourcesAndCheckpointMeta(t *testing.T) {
	for _, source := range []Source{Pins, UserIndex, Summary, Receipt, Tail, Recall} {
		t.Run(string(source), func(t *testing.T) {
			in := checkpoint.Input{Through: 4, ID: "cp", CurrentPos: 1}
			switch source {
			case UserIndex:
				in.Users = []checkpoint.UserMessage{{ID: 1, Message: msg("ABC-12345")}}
			case Summary:
				in.Summary = &checkpoint.Summary{Text: "ABC-12345"}
			case Receipt:
				in.Receipt = func(yield func(checkpoint.ReceiptItem) bool) {
					yield(checkpoint.ReceiptItem{ToolUseID: "action", Tool: "deploy", Args: "ABC-12345"})
				}
			case Pins, Tail, Recall:
			}
			cp, err := checkpoint.Render(in, index.RenderBudget{KeepUserTokens: 200, MaxSummaryTokens: 100, ReceiptTokens: 200, FramingTokens: 300}, checkpoint.DefaultLimits, nil)
			require.NoError(t, err)
			messages := []*model.Message{cp, msg("NEED:")}
			switch source {
			case Pins:
				messages = append([]*model.Message{msg("ABC-12345")}, messages...)
			case Tail:
				messages = append(messages, msg("ABC-12345"))
			case Recall:
				messages = append(messages, recallResult("ABC-12345"))
			case UserIndex, Summary, Receipt:
			}
			r := record(call(1, messages...))
			r.Runs[0].Transcript = []*model.Message{answer("ABC-12345")}
			got, err := Classify([]*runrecord.RunRecord{r}, spec(), true)
			require.NoError(t, err)
			assert.Contains(t, got.Sources, source)
			assert.False(t, got.AbsentWhenNeeded)
			if source == Summary {
				cp.Meta = nil
				got, err = Classify([]*runrecord.RunRecord{r}, spec(), true)
				require.NoError(t, err)
				assert.NotContains(t, got.Sources, Summary)
				assert.Contains(t, got.Sources, Tail)
			}
		})
	}
}
func TestMultipleNeedsAndLateRecall(t *testing.T) {
	s := spec()
	s.Evidence["second"] = Evidence{Value: "DEF-67890"}
	s.Needs = append(s.Needs, Need{Trigger: "SECOND:", Evidence: []EvidenceID{"second"}})
	s.Answer = append(s.Answer, "DEF-67890")
	r := record(call(2, msg("NEED:")), call(3, msg("SECOND:"), recallResult("ABC-12345")))
	r.Runs[0].Transcript = []*model.Message{answer("ABC-12345 DEF-67890"), recallResult("DEF-67890")}
	got, err := Classify([]*runrecord.RunRecord{r}, s, true)
	require.NoError(t, err)
	assert.True(t, got.NeedFound)
	assert.False(t, got.RecallSuccess)
	r.Runs[0].Calls = append(r.Runs[0].Calls, call(4, recallResult("ABC-12345 DEF-67890")))
	got, err = Classify([]*runrecord.RunRecord{r}, s, true)
	require.NoError(t, err)
	assert.True(t, got.RecallSuccess)
	r.Runs = append(r.Runs, runrecord.Run{RunID: "r2", Calls: []runrecord.Call{call(1, recallResult("DEF-67890"))}})
	r.Runs[0].Calls = r.Runs[0].Calls[:2]
	got, err = Classify([]*runrecord.RunRecord{r}, s, true)
	require.NoError(t, err)
	assert.False(t, got.RecallSuccess)
}

func TestSessionFinalAnswerIsRootNotChild(t *testing.T) {
	r := record(call(1, msg("NEED: ABC-12345")))
	r.RootRunID = "r1"
	r.Runs[0].Transcript = []*model.Message{answer("ABC-12345")}
	r.Runs = append(r.Runs, runrecord.Run{RunID: "child", Transcript: []*model.Message{answer("unrelated child response")}})
	got, err := Classify([]*runrecord.RunRecord{r}, spec(), true)
	require.NoError(t, err)
	assert.True(t, got.Used)
}

func TestRecallRequiresEveryAbsentItem(t *testing.T) {
	s := spec()
	s.Evidence["second"] = Evidence{Value: "DEF-67890"}
	s.Needs[0].Evidence = append(s.Needs[0].Evidence, "second")
	s.Answer = append(s.Answer, "DEF-67890")
	r := record(call(1, msg("NEED:")), call(2, recallResult("ABC-12345")))
	r.Runs[0].Transcript = []*model.Message{answer("ABC-12345 DEF-67890")}
	got, err := Classify([]*runrecord.RunRecord{r}, s, true)
	require.NoError(t, err)
	require.Len(t, got.Needs, 1)
	assert.ElementsMatch(t, []EvidenceID{"fact", "second"}, got.Needs[0].Absent)
	assert.Equal(t, []EvidenceID{"fact"}, got.Needs[0].Recalled)
	assert.True(t, got.Used)
	assert.True(t, got.RecallQualifying)
	assert.False(t, got.RecallSuccess)
}
