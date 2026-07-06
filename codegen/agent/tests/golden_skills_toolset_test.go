package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

func TestGolden_SkillsToolset_RegisterUsedToolsets(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.SkillsToolset())
	registry := fileContent(t, files, "gen/alpha/agents/scribe/registry.go")

	require.Contains(t, registry, `agentsruntime.NewSkillToolsetRegistration(agentsruntime.SkillToolsetConfig{`)
	require.Contains(t, registry, `Name: "alpha.skills",`)
	require.Contains(t, registry, `Roots: []string{`)
	require.Contains(t, registry, `".agents/skills",`)
	require.Contains(t, registry, `"shared/skills",`)
	require.NotContains(t, registry, `WithSkillsExecutor`)
	require.NotContains(t, registry, `"fmt"`)
	require.NotContains(t, registry, `runtime/agent/planner`)
}
