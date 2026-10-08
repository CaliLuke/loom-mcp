package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	"github.com/stretchr/testify/require"
)

func TestGoifyCollidingUnionVariantsRejectedDuringDSLValidation(t *testing.T) {
	design := func() {
		API("alpha", func() {})
		var Payload = Type("Payload", func() {
			OneOf("choice", func() {
				Attribute("foo_bar", String)
				Attribute("fooBar", Int32)
			})
			Required("choice")
		})
		var Result = Type("Result", func() {
			OneOf("outcome", func() {
				Attribute("foo_bar", String)
				Attribute("fooBar", Int32)
			})
			Required("outcome")
		})
		Service("alpha", func() {
			Agent("scribe", "", func() {
				Use("lookup", func() {
					Tool("pick", "", func() {
						Args(Payload)
						Return(Result)
					})
				})
			})
		})
	}

	testhelpers.SetupEvalRoots(t)
	require.True(t, eval.Execute(design, nil), eval.Context.Error())
	err := eval.RunDSL()
	require.Error(t, err)
	require.ErrorContains(t, err, `Args union "choice"`)
	require.ErrorContains(t, err, `Return union "outcome"`)
	require.ErrorContains(t, err, `"foo_bar"`)
	require.ErrorContains(t, err, `"fooBar"`)
}
