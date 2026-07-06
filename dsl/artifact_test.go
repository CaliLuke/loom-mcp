package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/expr/agent"
	"github.com/stretchr/testify/require"
)

func TestFromArtifactsDSL(t *testing.T) {
	runDSL(t, func() {
		Toolset("artifacts", FromArtifacts(MaxArtifactBytes(65536), MaxArtifacts(50)))
	})

	require.Len(t, agentsexpr.Root.Toolsets, 1)
	ts := agentsexpr.Root.Toolsets[0]
	require.Equal(t, "artifacts", ts.Name)
	require.NotNil(t, ts.Provider)
	require.Equal(t, agentsexpr.ProviderArtifacts, ts.Provider.Kind)
	require.Equal(t, 65536, ts.Provider.ArtifactMaxBytes)
	require.Equal(t, 50, ts.Provider.ArtifactMaxCount)
}
