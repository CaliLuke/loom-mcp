package codegen

import (
	"testing"

	"github.com/CaliLuke/loom/http/codegen/openapi"
	"github.com/stretchr/testify/require"
)

func TestRootDefinitionJSONPropagatesMarshalError(t *testing.T) {
	def := &openapi.Schema{
		DefaultValue: func() {},
	}
	definitions := map[string]*openapi.Schema{
		"Node": def,
	}

	_, err := rootDefinitionJSON(def, "Node", definitions)

	require.Error(t, err)
	require.ErrorContains(t, err, "unsupported type: func()")
}
