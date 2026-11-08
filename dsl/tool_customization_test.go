package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolNamedCustomizationOwnsRequiredFields(t *testing.T) {
	calls := 0
	runProjectionDSL(t, func() {
		API("test", func() {})
		doc := Type("Doc", func() {
			Attribute("id", String)
			Attribute("title", String)
			Required("id")
		})
		Service("assistant", func() {
			Agent("planner", "Planner", func() {
				Use("docs", func() {
					Tool("store", "Store", func() {
						Args(doc, func() {
							calls++
							Required("title")
						})
						Return(doc)
					})
				})
			})
		})
	})
	assert.Equal(t, 1, calls)
	tool := agentsexpr.Root.Agents[0].Used.Toolsets[0].Tools[0]
	payload, ok := tool.Args.Type.(goaexpr.UserType)
	require.True(t, ok)
	result, ok := tool.Return.Type.(goaexpr.UserType)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"id", "title"}, payload.Attribute().AllRequired())
	assert.Equal(t, []string{"id"}, result.Attribute().AllRequired())
	assert.NotEqual(t, payload.Name(), result.Name())
}
