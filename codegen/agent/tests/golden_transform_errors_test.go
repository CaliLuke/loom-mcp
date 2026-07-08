package tests

import (
	"testing"

	agentcodegen "github.com/CaliLuke/loom-mcp/codegen/agent"
	"github.com/CaliLuke/loom-mcp/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

func TestMethodBackedPayloadTransformIncompatibilityFailsGenerate(t *testing.T) {
	err := generateError(t, incompatiblePayloadTransformDesign)

	require.Error(t, err)
	require.ErrorContains(t, err, `method-backed tool "lookup.by_id" in toolset "alpha.lookup"`)
	require.ErrorContains(t, err, "failed to build payload transform")
	require.ErrorContains(t, err, "LookupPayload")
	require.ErrorContains(t, err, "DoPayload")
	require.ErrorContains(t, err, "in is a string but out type is int")
}

func TestMethodBackedResultTransformIncompatibilityFailsGenerate(t *testing.T) {
	err := generateError(t, incompatibleResultTransformDesign)

	require.Error(t, err)
	require.ErrorContains(t, err, `method-backed tool "lookup.by_id" in toolset "alpha.lookup"`)
	require.ErrorContains(t, err, "failed to build result transform")
	require.ErrorContains(t, err, "DoResult")
	require.ErrorContains(t, err, "LookupResult")
	require.ErrorContains(t, err, "in is a int but out type is string")
}

func generateError(t *testing.T, design func()) error {
	t.Helper()
	genpkg, roots := testhelpers.RunDesign(t, design)
	_, err := agentcodegen.Generate(genpkg, roots, nil)
	return err
}

func incompatiblePayloadTransformDesign() {
	API("alpha", func() {})
	var LookupPayload = Type("LookupPayload", func() {
		Attribute("q", String, "Query")
		Required("q")
	})
	var LookupResult = Type("LookupResult", func() {
		Attribute("ok", Boolean, "OK")
		Required("ok")
	})
	Service("alpha", func() {
		Method("Do", func() {
			Payload(func() {
				Attribute("q", Int, "Query")
				Required("q")
			})
			Result(LookupResult)
		})
		Agent("scribe", "Doc helper", func() {
			Use("lookup", func() {
				Tool("by_id", "Lookup by ID", func() {
					Args(LookupPayload)
					Return(LookupResult)
					BindTo("alpha", "Do")
				})
			})
		})
	})
}

func incompatibleResultTransformDesign() {
	API("alpha", func() {})
	var LookupPayload = Type("LookupPayload", func() {
		Attribute("q", String, "Query")
		Required("q")
	})
	var LookupResult = Type("LookupResult", func() {
		Attribute("score", String, "Score")
		Required("score")
	})
	Service("alpha", func() {
		Method("Do", func() {
			Payload(LookupPayload)
			Result(func() {
				Attribute("score", Int, "Score")
				Required("score")
			})
		})
		Agent("scribe", "Doc helper", func() {
			Use("lookup", func() {
				Tool("by_id", "Lookup by ID", func() {
					Args(LookupPayload)
					Return(LookupResult)
					BindTo("alpha", "Do")
				})
			})
		})
	})
}
