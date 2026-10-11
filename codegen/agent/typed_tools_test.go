package codegen

import (
	"testing"

	"github.com/CaliLuke/loom/codegen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocateToolDescriptorsRequiresTypedCodecs(t *testing.T) {
	codec := &typeData{GenerateCodec: true, ExportedCodec: "ValueCodec"}
	for _, tc := range []struct {
		name    string
		payload *typeData
		result  *typeData
		wantErr string
	}{
		{"typed codecs", codec, codec, ""},
		{"missing payload", nil, codec, "payload"},
		{"missing result", codec, nil, "result"},
		{"untyped payload", &typeData{}, codec, "payload"},
		{"untyped result", codec, &typeData{}, "result"},
		{"unnamed payload codec", &typeData{GenerateCodec: true}, codec, "payload"},
		{"unnamed result codec", codec, &typeData{GenerateCodec: true}, "result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := &toolEntry{Name: "lookup.search", ConstName: "Search", Payload: tc.payload, Result: tc.result}
			err := allocateToolDescriptors([]*toolEntry{entry}, codegen.NewNameScope())
			if tc.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, "lookup.search")
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "SearchTool", entry.DescriptorName)
		})
	}
}

func TestAllocateToolDescriptorsPreservesAllocatedNames(t *testing.T) {
	scope := codegen.NewNameScope()
	assert.Equal(t, "Search", scope.Unique("Search"))
	assert.Equal(t, "SearchTool", scope.Unique("SearchTool"))
	assert.Equal(t, "SearchToolTool", scope.Unique("SearchToolTool"))
	codec := &typeData{GenerateCodec: true, ExportedCodec: "ValueCodec"}
	entries := []*toolEntry{
		{Name: "lookup.search", ConstName: "Search", Payload: codec, Result: codec},
		{Name: "lookup.search_tool", ConstName: "SearchTool", Payload: codec, Result: codec},
	}
	require.NoError(t, allocateToolDescriptors(entries, scope))
	assert.Equal(t, "SearchTool2", entries[0].DescriptorName)
	assert.Equal(t, "SearchToolTool2", entries[1].DescriptorName)
	assert.Equal(t, "Search", entries[0].ConstName)
	assert.Equal(t, "SearchTool", entries[1].ConstName)
}

func TestAllocateToolDescriptorsPreservesDerivedNames(t *testing.T) {
	for _, prefix := range []string{"Spec", "Inject", "Decode", "With"} {
		t.Run(prefix, func(t *testing.T) {
			scope := codegen.NewNameScope()
			codec := &typeData{GenerateCodec: true, ExportedCodec: "ValueCodec"}
			entries := []*toolEntry{
				{Name: "lookup.derived", ConstName: prefix + "Lookup", Payload: codec, Result: codec},
				{Name: "lookup.lookup_tool", ConstName: "LookupTool", Payload: codec, Result: codec},
			}
			for _, entry := range entries {
				assert.Equal(t, entry.ConstName, scope.Unique(entry.ConstName))
			}
			require.NoError(t, allocateToolDescriptors(entries, scope))
			assert.Equal(t, prefix+"LookupTool2", entries[0].DescriptorName)
			assert.Equal(t, prefix+"Lookup", entries[0].ConstName)
			assert.Equal(t, "LookupTool", entries[1].ConstName)
		})
	}
}
