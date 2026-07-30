package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// ArtifactsToolset references runtime artifact tools as model-facing tools.
func ArtifactsToolset() func() {
	return func() {
		API("alpha", func() {})
		var Artifacts = Toolset("artifacts", FromArtifacts(MaxArtifactBytes(65536), MaxArtifacts(50)))
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use(Artifacts)
			})
		})
	}
}
