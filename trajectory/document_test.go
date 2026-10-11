package trajectory_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentGoldensAndSchema(t *testing.T) {
	schema := compileTrajectorySchema(t)
	goldens, err := os.OpenRoot("testdata/golden")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, goldens.Close())
	})
	for name, runs := range renderFixtures(t) {
		t.Run(name, func(t *testing.T) {
			var data []byte
			var err error
			if len(runs) == 1 {
				data, err = trajectory.EncodeDocument(runs[0])
			} else {
				data, err = trajectory.EncodeSessionDocument(runs)
			}
			require.NoError(t, err)
			// .document.golden deliberately avoids testutil's float64-based JSON
			// normalization so numeric tokens in document goldens remain exact.
			path := filepath.Join("testdata", "golden", name+".document.golden")
			testutil.AssertString(t, path, string(data))
			require.NoError(t, schema.Validate(schemaValue(t, data)))
			golden, err := goldens.ReadFile(name + ".document.golden")
			require.NoError(t, err)
			assert.NoError(t, schema.Validate(schemaValue(t, golden)))
			assert.Contains(t, string(data), "9007199254740993")
		})
	}
}

func TestDocumentEncodingParity(t *testing.T) {
	for name, runs := range renderFixtures(t) {
		t.Run(name, func(t *testing.T) {
			tr, err := runs[0].Marshal()
			require.NoError(t, err)
			want, err := callrecord.Encode(map[string]any{"schema": trajectory.SchemaVersion, "trajectory": json.RawMessage(tr)})
			require.NoError(t, err)
			got, err := trajectory.EncodeDocument(runs[0])
			require.NoError(t, err)
			assert.Equal(t, want, got)
			again, err := trajectory.EncodeDocument(runs[0])
			require.NoError(t, err)
			assert.Equal(t, got, again)
			session, err := runs.Marshal()
			require.NoError(t, err)
			want, err = callrecord.Encode(map[string]any{"schema": trajectory.SchemaVersion, "session": json.RawMessage(session)})
			require.NoError(t, err)
			got, err = trajectory.EncodeSessionDocument(runs)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
	_, err := trajectory.EncodeDocument(nil)
	require.Error(t, err)
	_, err = trajectory.EncodeSessionDocument(nil)
	require.Error(t, err)
	empty, err := trajectory.EncodeSessionDocument(trajectory.Session{})
	require.NoError(t, err)
	assert.NoError(t, compileTrajectorySchema(t).Validate(schemaValue(t, empty)))
}

func TestSchemaRejectsInvalidDocuments(t *testing.T) {
	schema := compileTrajectorySchema(t)
	tr := resultTrajectory("null")
	for _, test := range []struct {
		name  string
		value any
	}{
		{"neither member", trajectory.Document{Schema: trajectory.SchemaVersion}},
		{"both members", trajectory.Document{Schema: trajectory.SchemaVersion, Trajectory: tr, Session: trajectory.Session{tr}}},
		{"wrong version", trajectory.Document{Schema: "loom.trajectory.v2", Trajectory: tr}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := callrecord.Encode(test.value)
			require.NoError(t, err)
			assert.Error(t, schema.Validate(schemaValue(t, data)))
		})
	}
	for _, step := range []trajectory.Step{
		{Kind: trajectory.StepModel, Tool: &trajectory.ToolStep{}},
		{Kind: trajectory.StepTool, Tool: &trajectory.ToolStep{}, Model: &trajectory.ModelStep{}},
		{Kind: trajectory.StepEvent},
		{Kind: "unknown", Event: &trajectory.ObservedEvent{}},
	} {
		tr.Steps = []trajectory.Step{step}
		data, err := trajectory.EncodeDocument(tr)
		require.NoError(t, err)
		assert.Error(t, schema.Validate(schemaValue(t, data)))
	}
}

func TestSchemaReturnsOwnedBytes(t *testing.T) {
	first := trajectory.Schema()
	want := bytes.Clone(first)
	require.NotEmpty(t, first)
	first[0] = '!'
	assert.Equal(t, want, trajectory.Schema())
}

func TestSchemaFinalMessageVariants(t *testing.T) {
	schema := compileTrajectorySchema(t)
	for _, part := range []model.Part{
		model.TextPart{Text: "text"}, model.ThinkingPart{Text: "thinking"},
		model.ImagePart{Format: "png", Bytes: []byte{1}},
		model.DocumentPart{Name: "document", URI: "https://example.com/doc"},
		model.CitationsPart{Text: "cited", Citations: []model.Citation{{Location: model.CitationLocation{DocumentChar: &model.DocumentCharLocation{DocumentIndex: 1, Start: 1, End: 2}}}}},
		model.ToolUsePart{ID: "call", Name: "tool", Input: json.RawMessage(`{"large":9007199254740993}`)},
		model.ToolResultPart{ToolUseID: "call", Content: json.RawMessage(`{"fraction":1.2300}`)},
		model.CacheCheckpointPart{},
	} {
		t.Run(fmt.Sprintf("%T", part), func(t *testing.T) {
			tr := resultTrajectory("null")
			tr.Final = &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{part}}
			data, err := trajectory.EncodeDocument(tr)
			require.NoError(t, err)
			assert.NoError(t, schema.Validate(schemaValue(t, data)))
		})
	}
}

func compileTrajectorySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("trajectory.v1.schema.json", schemaValue(t, trajectory.Schema())))
	schema, err := compiler.Compile("trajectory.v1.schema.json")
	require.NoError(t, err)
	return schema
}

func schemaValue(t *testing.T, data []byte) any {
	t.Helper()
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(t, err)
	return value
}
