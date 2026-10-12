package model

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultPartsK7BytesUnchanged(t *testing.T) {
	original, err := os.ReadFile("../runtime/testdata/k7_messages.json")
	require.NoError(t, err)
	var messages map[string][]*Message
	require.NoError(t, json.Unmarshal(original, &messages))
	for _, sequence := range messages {
		for _, message := range sequence {
			if message == nil {
				continue
			}
			for _, part := range message.Parts {
				if result, ok := part.(ToolResultPart); ok {
					assert.Nil(t, result.Parts)
				}
			}
		}
	}
	encoded, err := json.Marshal(messages, json.Deterministic(true))
	require.NoError(t, err)
	raw := jsontext.Value(encoded)
	require.NoError(t, raw.Format(jsontext.ReorderRawObjects(true)))
	assert.True(t, bytes.Equal(bytes.TrimSpace(original), raw), "canonical K7 bytes changed")
}
