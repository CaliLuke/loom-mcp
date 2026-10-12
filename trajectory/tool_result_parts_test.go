package trajectory_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaToolResultParts(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("trajectory.v1.schema.json", schemaValue(t, trajectory.Schema())))
	schema, err := compiler.Compile("trajectory.v1.schema.json#/$defs/ToolResultPart")
	require.NoError(t, err)
	for _, nested := range []string{`{"Kind":"image","Format":"png","Bytes":"AQ=="}`, `{"Kind":"document","Name":"pdf","Format":"pdf","Bytes":"AQ==","Text":"","Chunks":null,"URI":"","Context":"","Cite":false}`} {
		data := []byte(`{"Kind":"tool_result","ToolUseID":"call","Content":null,"IsError":false,"Parts":[` + nested + `]}`)
		assert.NoError(t, schema.Validate(schemaValue(t, data)))
	}
}

func TestSchemaProtocol18SupportDocument(t *testing.T) {
	tr := renderFixtures(t)["tree"][0]
	support := model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{model.ImageFormatPNG}, DocumentFormats: []model.DocumentFormat{model.DocumentFormatPDF}, DocumentURISchemes: []string{"s3"}, MaxImageBytes: 100, MaxDocumentBytes: 100}
	found := false
	for i := range tr.Steps {
		if tr.Steps[i].Model != nil && tr.Steps[i].Model.Facts != nil {
			tr.Steps[i].Model.Facts.ToolResultParts = support
			found = true
		}
	}
	require.True(t, found)
	data, err := trajectory.EncodeDocument(tr)
	require.NoError(t, err)
	assert.NoError(t, compileTrajectorySchema(t).Validate(schemaValue(t, data)))
	assert.Contains(t, string(data), `"tool_result_parts"`)
}
