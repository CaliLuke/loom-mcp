package assistantapi

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	assistant "example.com/assistant/gen/assistant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func FuzzGeneratedUnionJSONCodecs(f *testing.F) {
	f.Add([]byte(`{"action":"list","value":{"limit":3}}`))
	f.Add([]byte(`{"action":"create","value":{"name":"loom"}}`))
	f.Add([]byte(`{"action":"foo","args":{"label":"loom"}}`))
	f.Add([]byte(`{"action":"bar","args":{"count":2}}`))
	f.Add([]byte(`{"action":"unknown","value":{}}`))
	f.Add([]byte(`{"action":"create","value":null}`))
	f.Add([]byte(`{`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		createOrListErr := fuzzCreateOrListUnion(t, data)
		barOrFooErr := fuzzBarOrFooUnion(t, data)
		if !jsontext.Value(data).IsValid() {
			require.Error(t, createOrListErr)
			require.Error(t, barOrFooErr)
			return
		}
		var tag struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(data, &tag); err != nil {
			require.Error(t, createOrListErr)
			require.Error(t, barOrFooErr)
			return
		}
		if tag.Action != "list" && tag.Action != "create" {
			require.Error(t, createOrListErr)
		}
		if tag.Action != "foo" && tag.Action != "bar" {
			require.Error(t, barOrFooErr)
		}
	})
}

func fuzzCreateOrListUnion(t *testing.T, data []byte) error {
	t.Helper()
	var decoded assistant.CreateActionOrListAction
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	var roundTrip assistant.CreateActionOrListAction
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	assert.Equal(t, decoded.Kind(), roundTrip.Kind())
	assert.Equal(t, decoded.ListAction, roundTrip.ListAction)
	assert.Equal(t, decoded.CreateAction, roundTrip.CreateAction)
	return nil
}

func fuzzBarOrFooUnion(t *testing.T, data []byte) error {
	t.Helper()
	var decoded assistant.BarCmdOrFooCmd
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	var roundTrip assistant.BarCmdOrFooCmd
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	assert.Equal(t, decoded.Kind(), roundTrip.Kind())
	assert.Equal(t, decoded.FooCmd, roundTrip.FooCmd)
	assert.Equal(t, decoded.BarCmd, roundTrip.BarCmd)
	return nil
}
