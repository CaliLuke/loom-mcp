package codegen

import (
	"errors"
	"testing"

	"github.com/CaliLuke/loom/codegen/service"
	"github.com/stretchr/testify/require"
)

func TestToolsetSpecsFilesReturnsToolSpecsDataBuildError(t *testing.T) {
	sentinel := errors.New("sentinel specs build failure")
	agent, cache := agentErrorPropagationFixture(sentinel)
	data := &GeneratorData{
		Genpkg: agent.Genpkg,
		Services: []*ServiceAgentsData{
			{Service: agent.Service, Agents: []*AgentData{agent}},
		},
	}

	files, err := toolsetSpecsFiles(data, cache)

	require.Nil(t, files)
	require.ErrorIs(t, err, sentinel)
	require.Contains(t, err.Error(), `agent codegen: build toolset specs for toolset "helpers"`)
}

func TestAgentSpecsJSONFileReturnsToolSpecsDataBuildError(t *testing.T) {
	sentinel := errors.New("sentinel specs build failure")
	agent, cache := agentErrorPropagationFixture(sentinel)

	file, err := agentSpecsJSONFile(agent, cache)

	require.Nil(t, file)
	require.ErrorIs(t, err, sentinel)
	require.Contains(t, err.Error(), `loom-mcp: tool schema generation failed for agent "scribe"`)
}

func TestAgentToolsFilesReturnsToolSpecsDataBuildError(t *testing.T) {
	sentinel := errors.New("sentinel specs build failure")
	agent, cache := agentErrorPropagationFixture(sentinel)

	files, err := agentToolsFiles(agent, cache)

	require.Nil(t, files)
	require.ErrorIs(t, err, sentinel)
	require.Contains(t, err.Error(), `agent codegen: build exported toolset specs for agent "scribe" toolset "helpers"`)
}

func TestUsedToolsFilesReturnsToolSpecsDataBuildError(t *testing.T) {
	sentinel := errors.New("sentinel specs build failure")
	agent, cache := agentErrorPropagationFixture(sentinel)

	files, err := usedToolsFiles(agent, cache)

	require.Nil(t, files)
	require.ErrorIs(t, err, sentinel)
	require.Contains(t, err.Error(), `agent codegen: build used toolset specs for agent "scribe" toolset "helpers"`)
}

func TestToolsetAdapterTransformsFileReturnsToolSpecsDataBuildError(t *testing.T) {
	sentinel := errors.New("sentinel specs build failure")
	agent, cache := agentErrorPropagationFixture(sentinel)

	file, err := toolsetAdapterTransformsFile(agent.Genpkg, agent.AllToolsets[0], cache)

	require.Nil(t, file)
	require.ErrorIs(t, err, sentinel)
	require.Contains(t, err.Error(), `agent codegen: build transform specs for toolset "helpers"`)
}

func agentErrorPropagationFixture(err error) (*AgentData, *toolSpecsDataCache) {
	svc := &service.Data{Name: "alpha"}
	toolset := &ToolsetData{
		Name:              "helpers",
		QualifiedName:     "helpers",
		SourceService:     svc,
		SourceServiceName: svc.Name,
		SpecsDir:          "gen/alpha/toolsets/helpers",
		SpecsImportPath:   "example.com/assistant/alpha/toolsets/helpers",
		SpecsPackageName:  "helpers_specs",
		AgentToolsDir:     "gen/alpha/agents/scribe/agenttools/helpers",
		AgentToolsPackage: "helpers_agenttools",
		PackageName:       "helpers",
		Dir:               "gen/alpha/toolsets/helpers",
		Tools: []*ToolData{
			{Name: "summarize", QualifiedName: "helpers.summarize"},
		},
	}
	toolset.Tools[0].Toolset = toolset
	agent := &AgentData{
		Genpkg:               "example.com/assistant",
		Name:                 "scribe",
		Service:              svc,
		AllToolsets:          []*ToolsetData{toolset},
		ExportedToolsets:     []*ToolsetData{toolset},
		MethodBackedToolsets: []*ToolsetData{toolset},
		Tools:                toolset.Tools,
	}
	toolset.Agent = agent

	cache := newToolSpecsDataCache()
	cache.build = func(genpkg string, svc *service.Data, tools []*ToolData) (*toolSpecsData, error) {
		return nil, err
	}
	return agent, cache
}
