package agentfeatures_test

import (
	"encoding/json/v2"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func assertSupportDomain(t *testing.T, facts supportFacts, run supportRun) {
	t.Helper()
	var outcome supportOutcome
	require.NoError(t, json.Unmarshal(run.outputs["receipt.json"], &outcome))
	assert.Equal(t, facts, outcome.Analysis.Facts)
	assert.Equal(t, supportDigest(supportJSON(facts)), outcome.Analysis.SourceDigest)
	assert.Contains(t, outcome.Body, outcome.Analysis.Summary)
	assert.Contains(t, outcome.Body, outcome.Review)
	assert.Contains(t, outcome.Body, facts.ID)
	assert.Contains(t, outcome.Body, facts.Label)
	assert.Contains(t, outcome.Body, facts.Audience)
	if facts.Kind == "document" {
		assert.Equal(t, 2, outcome.Version)
		assert.Contains(t, string(run.outputs["document.md"]), facts.Protected)
		assert.Equal(t, outcome.Body, string(run.outputs["document.md"]))
	} else {
		assert.Equal(t, 1, outcome.Version)
		assert.True(t, strings.HasPrefix(string(run.outputs["release.md"]), "### What's included\n"))
		assert.Equal(t, outcome.Body, string(run.outputs["release.md"]))
		require.Len(t, outcome.Analysis.Facts.Nodes, 3)
		require.Len(t, outcome.Analysis.Facts.Edges, 2)
	}
}

// assertSupportChild uses private canonical evidence; public projections intentionally hide systems.
func assertSupportChild(t *testing.T, env *supportEnvironment, run supportRun, instruction string) {
	t.Helper()
	reopened, err := eval.Open(filepath.Join(env.root, "experiments"))
	require.NoError(t, err)
	evidence, err := reopened.ReadEvidence(t.Context(), run.record.ExperimentID)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	require.Empty(t, evidence[0].Error)
	require.NotNil(t, evidence[0].Snapshot)
	require.Len(t, evidence[0].Snapshot.Records, 1)
	record := evidence[0].Snapshot.Records[0]
	require.Len(t, record.Runs, 2, "real generated delegated child must execute")
	parent, child := record.Runs[0], record.Runs[1]
	assert.NotEqual(t, parent.RunID, child.RunID)
	linked := false
	for _, event := range parent.Events {
		if strings.Contains(string(event.Payload), `"ChildRunID":"`+child.RunID+`"`) {
			linked = true
		}
	}
	assert.True(t, linked, "canonical child link must identify actual descendant")
	require.Len(t, child.Calls, 1)
	request := child.Calls[0].Request
	require.NotNil(t, request)
	assert.Equal(t, child.RunID, request.RunID)
	var actualSystem string
	for _, message := range request.Messages {
		if message.Role == model.ConversationRoleSystem {
			actualSystem = experimentMessageText(message)
		}
	}
	assert.Equal(t, instruction, actualSystem)
	var used []string
	for _, message := range child.Transcript {
		for _, part := range message.Parts {
			if use, ok := part.(model.ToolUsePart); ok {
				used = append(used, use.Name)
			}
		}
	}
	expectedRead := "documents.read_document"
	if strings.Contains(string(run.outputs["receipt.json"]), `"kind":"graph"`) {
		expectedRead = "graph.call_tool"
	}
	assert.Equal(t, []string{expectedRead}, used, "child must perform one meaningful current-source read")
	for _, definition := range request.Tools {
		if definition.Name == tools.ToolUnavailable.String() || definition.Name == tools.RecallContext.String() {
			continue
		}
		assert.Equal(t, expectedRead, definition.Name, "host grants only the exact current-source read")
	}
	result, err := latestExperimentResult(request)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	var current supportFacts
	require.NoError(t, json.Unmarshal(supportJSON(result.Content), &current))

	var outcome supportOutcome
	require.NoError(t, json.Unmarshal(run.outputs["receipt.json"], &outcome))
	assert.Contains(t, string(supportJSON(request.Messages)), outcome.Analysis.Facts.ID)
	assert.Equal(t, outcome.Analysis.Facts, current)
	require.NotNil(t, child.Calls[0].Completion)
	require.NotNil(t, child.Calls[0].Completion.Response)
	response, err := callrecord.DecodeResponse(record.Content[*child.Calls[0].Completion.Response])
	require.NoError(t, err)
	require.Len(t, response.Content, 1)
	assert.Equal(t, experimentMessageText(&response.Content[0]), outcome.Review)
	assert.Contains(t, outcome.Review, strings.TrimSuffix(strings.TrimPrefix(instruction, "section="), "\n")+": ")
	consumed := false
	for _, call := range parent.Calls {
		require.NotNil(t, call.Request)
		assert.NotContains(t, string(supportJSON(call.Request.Messages)), "UNOPENED-PHYSICAL-SOURCE")
		if strings.Contains(string(supportJSON(call.Request.Messages)), outcome.Review) {
			consumed = true
		}
	}
	assert.True(t, consumed, "actual child output must enter a subsequent parent model request")
	assert.Contains(t, outcome.Body, outcome.Review)
}

func assertSupportFreshFacts(t *testing.T, env *supportEnvironment, kind string, run supportRun, selected control.Frozen) {
	t.Helper()
	a, b := env.facts[kind+"-A"], env.facts[kind+"-B"]
	all := string(supportJSON(run.outputs))
	for _, bytes := range run.outputs {
		all += string(bytes)
	}
	assert.NotContains(t, all, a.ID)
	assert.NotContains(t, all, a.Label)
	assert.NotContains(t, all, a.Audience)
	assert.Contains(t, all, b.ID)
	assert.Equal(t, 2, run.frozen.Manifest.Version)
	require.Len(t, run.frozen.Manifest.Origins, 2)
	for _, origin := range run.frozen.Manifest.Origins {
		assert.Equal(t, selected.Candidate, origin.SourceCandidate)
		assert.Contains(t, []string{"SKILL.md", "helper.go"}, origin.Name)
	}
}

func assertSupportUnsupportedHost(t *testing.T, env *supportEnvironment) {
	t.Helper()
	for _, variant := range []string{"binding", "activation", "grammar"} {
		t.Run(variant, func(t *testing.T) {
			surface := supportSurface()
			baseline := supportBaseline()
			switch variant {
			case "binding":
				surface.Slots[2].Binding.Version = "unadvertised"
			case "activation":
				surface.Slots[2].Activation = candidate.OnDemand
			case "grammar":
				baseline[2].Data = []byte("arbitrary executable child instructions")
			}
			store, err := candidate.Open(t.Context(), t.TempDir(), surface, baseline)
			require.NoError(t, err)
			frozen, err := store.Load(t.Context(), store.Discover().BaselineDigest)
			require.NoError(t, err)
			before := env.calls.Load()
			lease, err := env.prepareSupportAttempt(t.Context(), frozen, env.facts["document-A"], eval.PreparedAttemptInfo{Item: eval.Item{Input: "DOC-A"}})
			require.Error(t, err)
			assert.Nil(t, lease)
			assert.Equal(t, before, env.calls.Load())
		})
	}
}
