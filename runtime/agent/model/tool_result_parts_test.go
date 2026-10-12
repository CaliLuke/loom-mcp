package model

import (
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultPartsJSON(t *testing.T) {
	for _, discriminator := range []string{`"Kind":"tool_result",`, ""} {
		t.Run(discriminator, func(t *testing.T) {
			wire := `{"Role":"user","Parts":[{` + discriminator + `"ToolUseID":"call","Content":{"ok":true},"IsError":false,"Parts":[{"Kind":"image","Format":"png","Bytes":"AQI="},{"Kind":"document","Name":"pdf","Format":"pdf","Bytes":"AwQ="}]}]}`
			var msg Message
			require.NoError(t, json.Unmarshal([]byte(wire), &msg))
			encoded, err := json.Marshal(msg)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), `"Kind":"image"`)
			assert.Contains(t, string(encoded), `"Kind":"document"`)
		})
	}
}

func TestToolResultPartsInvalidJSON(t *testing.T) {
	for _, nested := range []string{`null`, `{"Kind":"text","Text":"bad"}`, `{"Kind":"tool_use","ID":"x","Name":"x"}`, `{"Kind":"thinking","Text":"bad"}`, `{"Kind":"cache_checkpoint"}`, `{"Kind":"document","Name":"doc","Text":"text"}`, `{"Kind":"document","Name":"doc","Chunks":["text"]}`, `{"Kind":"document","Name":"doc","Bytes":"AQ==","Cite":true}`, `{"Kind":"image","Format":"png"}`} {
		t.Run(nested, func(t *testing.T) {
			var msg Message
			err := json.Unmarshal([]byte(`{"Role":"user","Parts":[{"Kind":"tool_result","ToolUseID":"call","Parts":[`+nested+`]}]}`), &msg)
			assert.Error(t, err)
		})
	}
	var msg Message
	assert.Error(t, json.Unmarshal([]byte(`{"Role":"user","Parts":[{"Kind":"tool_result","ToolUseID":"call","IsError":true,"Parts":[{"Kind":"image","Format":"png","Bytes":"AQ=="}]}]}`), &msg))
}

func TestToolResultPartsLegacyJSON(t *testing.T) {
	wire := `{"Role":"user","Parts":[{"Kind":"tool_result","ToolUseID":"call","Content":{"ok":true},"IsError":false}],"Meta":{}}`
	var msg Message
	require.NoError(t, json.Unmarshal([]byte(wire), &msg))
	encoded, err := json.Marshal(msg)
	require.NoError(t, err)
	assert.JSONEq(t, wire, string(encoded))
	assert.NotContains(t, string(encoded), `"Parts":[]`)
}

func TestToolResultPartsClone(t *testing.T) {
	image := &ImagePart{Format: "png", Bytes: []byte{1, 2}}
	doc := &DocumentPart{Name: "pdf", Format: "pdf", Bytes: []byte{3, 4}}
	req := &Request{Messages: []*Message{{Role: ConversationRoleUser, Parts: []Part{ToolResultPart{ToolUseID: "call", Parts: []ToolResultContentPart{image, doc}}}}}}
	_, err := json.Marshal(req.Messages[0])
	require.NoError(t, err)
	owned, err := CloneRequest(req)
	require.NoError(t, err)
	image.Bytes[0] = 9
	doc.Bytes[0] = 9
	parts := owned.Messages[0].Parts[0].(ToolResultPart).Parts
	// Clone normalizes pointers, as it does for top-level parts.
	require.IsType(t, ImagePart{}, parts[0])
	assert.Equal(t, []byte{1, 2}, parts[0].(ImagePart).Bytes)
	assert.Equal(t, []byte{3, 4}, parts[1].(DocumentPart).Bytes)
	_, err = cloneModelPart(ToolResultPart{ToolUseID: "call", Parts: []ToolResultContentPart{image}}, &cloneBudget{bytes: maxModelOutputBytes - 4})
	assert.Error(t, err)
}

func TestToolResultPartsNilPointers(t *testing.T) {
	for _, part := range []ToolResultContentPart{nil, (*ImagePart)(nil), (*DocumentPart)(nil)} {
		req := &Request{Messages: []*Message{{Role: "user", Parts: []Part{ToolResultPart{ToolUseID: "call", Parts: []ToolResultContentPart{part}}}}}}
		_, err := CloneRequest(req)
		require.Error(t, err)
		_, err = json.Marshal(req.Messages[0])
		assert.Error(t, err)
	}
}

func TestToolResultPartsCloneSlotBound(t *testing.T) {
	image := &ImagePart{Format: "png", Bytes: []byte{1}}
	parts := make([]ToolResultContentPart, maxModelValueVisits+1)
	for i := range parts {
		parts[i] = image
	}
	// The slot bound must reject the container before allocating and cloning
	// its children. A byte-oversized first child distinguishes that preflight
	// from the later per-child visit bound.
	parts[0] = &ImagePart{Format: "png", Bytes: make([]byte, maxModelOutputBytes+1)}
	req := &Request{Messages: []*Message{{Role: ConversationRoleUser, Parts: []Part{
		ToolResultPart{ToolUseID: "call", Parts: parts},
	}}}}
	owned, err := CloneRequest(req)
	assert.Nil(t, owned)
	var bounds *modelBoundsError
	require.ErrorAs(t, err, &bounds)
	assert.Contains(t, err.Error(), "maximum visited values")
}

func TestToolResultPartsTokenEstimate(t *testing.T) {
	estimator := TokenEstimator{CharactersPerToken: 1, OverheadTokens: 1}
	result := ToolResultPart{ToolUseID: "call", Content: "image"}
	req := &Request{Messages: []*Message{{Role: ConversationRoleUser, Parts: []Part{result}}}}
	baseline, err := estimator.CountTokens(t.Context(), req)
	require.NoError(t, err)
	image := ImagePart{Format: "png", Bytes: make([]byte, 1024)}
	result.Parts = []ToolResultContentPart{image}
	req.Messages[0].Parts[0] = result
	withImage, err := estimator.CountTokens(t.Context(), req)
	require.NoError(t, err)
	assert.Greater(t, withImage.InputTokens, baseline.InputTokens)
	assert.Equal(t, len(image.Format)+len(image.Bytes), withImage.InputTokens-baseline.InputTokens)
	assert.False(t, withImage.Exact)
}
