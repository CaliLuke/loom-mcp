package contextmeasure

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultPartsMeasurement(t *testing.T) {
	messages := []*model.Message{{Role: "user", Parts: []model.Part{model.ToolResultPart{ToolUseID: "id", Content: "abcd", Parts: []model.ToolResultContentPart{
		&model.ImagePart{Format: "png", Bytes: []byte{1}},
		model.DocumentPart{Name: "doc", Context: "ctx", Format: "pdf", Bytes: []byte{1, 2, 3}},
		model.DocumentPart{Name: "uri", URI: "s3://bucket/key"},
	}}}}}
	facts, err := Measure(&model.Request{Messages: messages}, model.ContextLimits{ImageTokens: 100}, messages)
	require.NoError(t, err)
	assert.Equal(t, 17, facts.TextBytes)
	assert.Equal(t, 202, facts.NonText)
	assert.Equal(t, 17, facts.ViewTextBytes)
	assert.Equal(t, 202, facts.ViewNonText)
	facts.InputTokens = 207
	rate, ok := Calibrate(facts)
	assert.True(t, ok)
	assert.Equal(t, Rate{5, 17}, rate)
	size, err := MeasureMessages(messages)
	require.NoError(t, err)
	assert.Equal(t, 3002, size.NonText)
}
