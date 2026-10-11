package contextmeasure

import (
	jsonopts "encoding/json"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestMeasureMatchedViewFacts(t *testing.T) {
	view := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{
		model.TextPart{Text: "é"}, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1}},
		model.DocumentPart{Name: "pdf", Format: model.DocumentFormatPDF, Bytes: []byte{1, 2, 3}},
	}}
	matched := *view
	matched.Parts = append(append([]model.Part{}, view.Parts...), model.CacheCheckpointPart{})
	matched.Meta = map[string]any{"different": true}
	for _, tc := range []struct {
		name          string
		messages      []*model.Message
		text, nontext int
		bearing       bool
	}{
		{"matched", []*model.Message{&matched}, 5, 702, true},
		{"duplicate_request_occurrences", []*model.Message{&matched, &matched}, 10, 1404, true},
		{"unrelated", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "other"}}}}, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &model.Request{Messages: append([]*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "fixed"}}}}, tc.messages...)}
			facts, err := Measure(req, model.ContextLimits{ImageTokens: 700}, []*model.Message{view, view})
			require.NoError(t, err)
			assert.Equal(t, tc.text, facts.ViewTextBytes)
			assert.Equal(t, tc.nontext, facts.ViewNonText)
			assert.Equal(t, tc.bearing, facts.ViewBearing)
			assert.Equal(t, 5, facts.FixedBytes)
		})
	}
}

func TestViewFactsJSONRoundTrip(t *testing.T) {
	facts := RequestFacts{Model: "m", TextBytes: 12, NonText: 700, ViewBearing: true, ViewTextBytes: 9, ViewNonText: 700}
	data, err := json.Marshal(facts, jsonopts.FormatDurationAsNano(true))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"view_text_bytes":9`)
	assert.Contains(t, string(data), `"view_non_text":700`)
	var restored RequestFacts
	require.NoError(t, json.Unmarshal(data, &restored, jsonopts.FormatDurationAsNano(true)))
	assert.Equal(t, facts, restored)
	// This is an old durable facts body, before the optional view counts existed.
	restored = RequestFacts{}
	require.NoError(t, json.Unmarshal([]byte(`{"model":"old","text_bytes":12,"view_bearing":true}`), &restored))
	assert.Zero(t, restored.ViewTextBytes)
	assert.Zero(t, restored.ViewNonText)
	data, err = json.Marshal(restored, jsonopts.FormatDurationAsNano(true))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "view_text_bytes")
	assert.NotContains(t, string(data), "view_non_text")
}
