package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

func TestGolden_ArtifactsToolset_RegisterUsedToolsets(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ArtifactsToolset())
	registry := fileContent(t, files, "gen/alpha/agents/scribe/registry.go")

	require.Contains(t, registry, `agentsruntime.NewArtifactToolsetRegistration(agentsruntime.ArtifactToolsetConfig{`)
	require.Contains(t, registry, `Store: rt.ArtifactStore,`)
	require.Contains(t, registry, `Name: "alpha.artifacts",`)
	require.Contains(t, registry, `MaxArtifactBytes: 65536,`)
	require.Contains(t, registry, `MaxArtifacts: 50,`)
}
