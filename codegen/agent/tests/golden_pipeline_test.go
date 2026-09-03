package tests

import (
	"testing"

	agentcodegen "github.com/CaliLuke/loom-mcp/v2/codegen/agent"
	"github.com/CaliLuke/loom-mcp/v2/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDesignMirrorsProductionPreparePhase(t *testing.T) {
	genpkg, roots := testhelpers.RunDesign(t, func() {
		API("assistant", func() {})
		payload := Type("Profile", func() {
			Attribute("name", String)
			Required("name")
		})
		result := Type("ProfileResult", func() {
			Attribute("saved", Boolean)
			Required("saved")
		})
		Service("assistant", func() {
			Method("upsert", func() {
				Payload(payload)
				Result(result)
			})
			Agent("chat", "Chat agent", func() {
				Use("profile", func() {
					Tool("upsert", "Upsert profile", func() {
						BindTo("assistant", "upsert")
					})
				})
			})
		})
	})

	files, err := agentcodegen.Generate(genpkg, roots, nil)
	require.NoError(t, err)
	assert.True(t, testhelpers.FileExists(files, "gen/assistant/toolsets/profile/transforms.go"))
}
