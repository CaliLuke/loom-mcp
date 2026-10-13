package contextcompat

import (
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func compatibilityFixture(t *testing.T, parts []model.Part) (*inmem.Store, Request, compaction.State, []index.MessageEntry) {
	t.Helper()
	store := inmem.New()
	raw, err := json.Marshal(model.Message{Role: model.ConversationRoleUser, Parts: parts})
	require.NoError(t, err)
	hash, err := store.Put(t.Context(), "root", raw)
	require.NoError(t, err)
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: hash, NonText: 1500}}
	facts := contextmeasure.RequestFacts{Call: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}, Model: "model", ProviderConfig: "self", Qualified: compaction.Evict, ViewBearing: true, ContextWindow: 200000, ToolResultParts: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png", "open-format"}, DocumentFormats: []model.DocumentFormat{"pdf"}, DocumentURISchemes: []string{"s3"}, MaxImageBytes: 2, MaxDocumentBytes: 4}}
	persistCompatibilityCall(t, store, facts, runstore.CallCompleted)
	state := compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{facts})
	return store, BuildRequest("root", "run", "", entries, entries, state), state, entries
}

func persistCompatibilityCall(t *testing.T, store runstore.Store, facts contextmeasure.RequestFacts, final runstore.CallState) {
	t.Helper()
	started, err := callrecord.Encode(callrecord.Started{Key: facts.Call, Model: facts.Model, Activity: callrecord.ActivityPlanner})
	require.NoError(t, err)
	record := runstore.CallRecord{Key: facts.Call, State: runstore.CallStarted, Started: started}
	require.NoError(t, store.PutCall(t.Context(), "root", record))
	if final == runstore.CallStarted {
		return
	}
	record.State = final
	record.Completion, err = callrecord.Encode(callrecord.Completion{Facts: &facts})
	require.NoError(t, err)
	require.NoError(t, store.PutCall(t.Context(), "root", record))
}

func TestCompatibilityExactOriginalParts(t *testing.T) {
	cases := []struct {
		name       string
		parts      []model.Part
		compatible bool
	}{
		{"upload at limit", []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1, 2}}}, true},
		{"same tokens larger upload", []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}}}, false},
		{"same bytes different format", []model.Part{model.ImagePart{Format: "jpeg", Bytes: []byte{1, 2}}}, false},
		{"open string format", []model.Part{model.ImagePart{Format: "open-format", Bytes: []byte{1, 2}}}, true},
		{"duplicates", []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}, model.ImagePart{Format: "png", Bytes: []byte{1}}}, true},
		{"later incompatible part", []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}, model.DocumentPart{Name: "document", Format: "csv", Bytes: []byte{1}}}, false},
		{"document upload", []model.Part{model.DocumentPart{Name: "document", Format: "pdf", Bytes: []byte{1, 2, 3, 4}}}, true},
		{"document too large", []model.Part{model.DocumentPart{Name: "document", Format: "pdf", Bytes: []byte{1, 2, 3, 4, 5}}}, false},
		{"uri ignores upload size and format", []model.Part{model.DocumentPart{Name: "document", Format: "unknown", URI: "s3://bucket/key"}}, true},
		{"unsupported uri scheme", []model.Part{model.DocumentPart{Name: "document", Format: "pdf", URI: "https://example.test/a"}}, false},
		{"malformed uri", []model.Part{model.DocumentPart{Name: "document", Format: "pdf", URI: "s3://%zz"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, request, _, _ := compatibilityFixture(t, tc.parts)
			result, err := Check(t.Context(), store, request)
			require.NoError(t, err)
			assert.NotEmpty(t, result.Ref)
			assert.True(t, result.Evidence.Checked)
			assert.Equal(t, tc.compatible, result.Evidence.Compatible)
			assert.Equal(t, request.Coverage, result.Evidence.Coverage)
			assert.Equal(t, request.Support, result.Evidence.Support)
			if !tc.compatible {
				assert.Equal(t, "too_large", result.Evidence.Reason)
				assert.Equal(t, "model", result.Evidence.Model)
			}
			require.NoError(t, Validate(t.Context(), store, request, result))
		})
	}
}

func TestCompatibilityCoverageAndSupportProvenance(t *testing.T) {
	store, request, state, entries := compatibilityFixture(t, []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}})
	require.NotEmpty(t, request.Coverage)
	require.NotEmpty(t, request.Support)
	require.Len(t, request.Calls, 1)
	result, err := Check(t.Context(), store, request)
	require.NoError(t, err)
	text := append(append([]index.MessageEntry{}, entries...), index.MessageEntry{ID: 2, Kind: index.Message, Bytes: 100})
	textRequest := BuildRequest("root", "run", "", text, text, state)
	assert.Equal(t, request.Coverage, textRequest.Coverage)
	next := append(append([]index.MessageEntry{}, entries...), index.MessageEntry{ID: 2, Kind: index.Message, Hash: entries[0].Hash, NonText: 1500})
	nextRequest := BuildRequest("root", "run", "", next, next, state)
	assert.NotEqual(t, request.Coverage, nextRequest.Coverage)
	require.Error(t, Validate(t.Context(), store, nextRequest, result))
	narrow := contextmeasure.RequestFacts{Model: "narrow", Call: runstore.CallKey{RunID: "run", Seq: 2, Attempt: 1}, Qualified: compaction.Evict, ViewBearing: true, ToolResultParts: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"jpeg"}, MaxImageBytes: 2}}
	persistCompatibilityCall(t, store, narrow, runstore.CallCompleted)
	state = compaction.Observe(state, []contextmeasure.RequestFacts{narrow})
	narrowed := BuildRequest("root", "run", "", entries, entries, state)
	assert.NotEqual(t, request.Support, narrowed.Support)
	require.Error(t, Validate(t.Context(), store, narrowed, result))
	checked, err := Check(t.Context(), store, narrowed)
	require.NoError(t, err)
	assert.False(t, checked.Evidence.Compatible)
	// No view watermark appears in the request: evicted originals stay covered.
	assert.Equal(t, request.Coverage, narrowed.Coverage)
}

func TestCompatibilityRejectsUnprovenEvidence(t *testing.T) {
	for _, mutation := range []string{"scope", "coverage", "support", "missing call", "started call", "missing content", "malformed content"} {
		t.Run(mutation, func(t *testing.T) {
			store, request, _, _ := compatibilityFixture(t, []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}})
			switch mutation {
			case "scope":
				request.Scope = "other"
			case "coverage":
				request.Coverage = "altered"
			case "support":
				request.Support = "altered"
			case "missing call":
				request.Calls = []runstore.CallKey{{RunID: "run", Seq: 999, Attempt: 1}}
			case "started call":
				facts := contextmeasure.RequestFacts{Model: "model", Call: runstore.CallKey{RunID: "run", Seq: 2, Attempt: 1}, ViewBearing: true}
				persistCompatibilityCall(t, store, facts, runstore.CallStarted)
				request.Calls = []runstore.CallKey{facts.Call}
			case "missing content":
				request.Pending[0].Hash = "sha256:missing"
			case "malformed content":
				hash, err := store.Put(t.Context(), "root", []byte("not json"))
				require.NoError(t, err)
				request.Pending[0].Hash = hash
			}
			_, err := Check(t.Context(), store, request)
			require.Error(t, err)
		})
	}
}

func TestCompatibilityTerminalCallProvenance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   runstore.CallState
		bearing bool
		valid   bool
	}{
		{"completed", runstore.CallCompleted, true, true},
		{"failed first overflow", runstore.CallFailed, true, true},
		{"started", runstore.CallStarted, true, false},
		{"non-view-bearing", runstore.CallCompleted, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, request, _, entries := compatibilityFixture(t, []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}})
			fact := contextmeasure.RequestFacts{Call: runstore.CallKey{RunID: "run", Seq: 2, Attempt: 1}, Model: "first", ViewBearing: tc.bearing, Qualified: compaction.Evict, Outcome: contextmeasure.OutcomeOverflow, ToolResultParts: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png"}, MaxImageBytes: 10}}
			persistCompatibilityCall(t, store, fact, tc.state)
			observed := fact
			observed.ViewBearing = true
			state := compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{observed})
			later := fact
			later.Call.Seq = 3
			later.Qualified = compaction.Summary
			later.ToolResultParts = model.ToolResultPartSupport{}
			later.Outcome = contextmeasure.OutcomeOK
			persistCompatibilityCall(t, store, later, runstore.CallCompleted)
			state = compaction.Observe(state, []contextmeasure.RequestFacts{later})
			request = BuildRequest(request.Scope, request.RunID, "", entries, entries, state)
			result, err := Check(t.Context(), store, request)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.True(t, result.Evidence.Compatible, "later success cannot replace first adopted failed-call support")
			require.Equal(t, []runstore.CallKey{fact.Call}, request.Calls)
			assert.Equal(t, compaction.Evict, compaction.ObservedQualifiedTier(state))
		})
	}
}

func TestCompatibilityCommittedPendingAndToolParts(t *testing.T) {
	store, request, state, entries := compatibilityFixture(t, []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}})
	seg, err := index.NewSegment("", "", entries)
	require.NoError(t, err)
	data, _, err := index.EncodeSegment(seg)
	require.NoError(t, err)
	head, err := store.Put(t.Context(), "root", data)
	require.NoError(t, err)
	parts, err := json.Marshal(&model.Message{Role: "user", Parts: []model.Part{model.DocumentPart{Name: "document", Format: "pdf", Bytes: []byte{1, 2, 3, 4, 5}}}})
	require.NoError(t, err)
	partsHash, err := store.Put(t.Context(), "root", parts)
	require.NoError(t, err)
	body, err := store.Put(t.Context(), "root", []byte(`"result"`))
	require.NoError(t, err)
	pending := []index.MessageEntry{{ID: 2, Kind: index.ToolResult, Hash: body, ToolUseIDs: []string{"call"}, Batch: 2, NonText: 1500, Result: &index.ResultProjection{Parts: partsHash}}}
	all := append(append([]index.MessageEntry{}, entries...), pending...)
	request = BuildRequest(request.Scope, request.RunID, head, pending, all, state)
	result, err := Check(t.Context(), store, request)
	require.NoError(t, err)
	assert.False(t, result.Evidence.Compatible, "new pending parts are not hidden behind the committed head")
	assert.Equal(t, "too_large", result.Evidence.Reason)
	require.NoError(t, Validate(t.Context(), store, request, result))
}

func TestCompatibilityRejectsMissingContentAfterIncompatibility(t *testing.T) {
	store, _, state, entries := compatibilityFixture(t, []model.Part{model.ImagePart{Format: "jpeg", Bytes: []byte{1}}})
	entries = append(entries, index.MessageEntry{ID: 2, Kind: index.Message, Hash: "sha256:missing", NonText: 1500})
	request := BuildRequest("root", "run", "", entries, entries, state)
	_, err := Check(t.Context(), store, request)
	require.Error(t, err, "an earlier unsupported part cannot mask missing later content")
}
func TestCompatibilityWitnessSortedAcrossAllParts(t *testing.T) {
	store, _, _, entries := compatibilityFixture(t, []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}, model.DocumentPart{Name: "file", Format: "pdf", Bytes: []byte{1}}})
	facts := []contextmeasure.RequestFacts{
		{Call: runstore.CallKey{RunID: "run", Seq: 2, Attempt: 1}, Model: "a", ViewBearing: true, ToolResultParts: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png", "jpeg"}, DocumentFormats: []model.DocumentFormat{"csv"}, MaxImageBytes: 100, MaxDocumentBytes: 100}},
		{Call: runstore.CallKey{RunID: "run", Seq: 3, Attempt: 1}, Model: "z", ViewBearing: true, ToolResultParts: model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"jpeg"}, DocumentFormats: []model.DocumentFormat{"pdf", "csv"}, MaxImageBytes: 100, MaxDocumentBytes: 100}},
	}
	persistCompatibilityCall(t, store, facts[0], runstore.CallCompleted)
	persistCompatibilityCall(t, store, facts[1], runstore.CallCompleted)
	state := compaction.Observe(compaction.State{}, facts)
	result, err := Check(t.Context(), store, BuildRequest("root", "run", "", entries, entries, state))
	require.NoError(t, err)
	assert.Equal(t, "a", result.Evidence.Model)
	assert.Equal(t, "too_large", result.Evidence.Reason)
}
