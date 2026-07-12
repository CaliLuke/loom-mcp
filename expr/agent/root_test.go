package agent

import (
	"errors"
	"testing"

	exprmcp "github.com/CaliLuke/loom-mcp/expr/mcp"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func preserveGlobalRoots(t *testing.T) {
	t.Helper()

	goaRoot := goaexpr.Root
	mcpRoot := exprmcp.Root
	t.Cleanup(func() {
		goaexpr.Root = goaRoot
		exprmcp.Root = mcpRoot
	})
}

func TestRootExprPrepareMaterializesSelectedOriginTools(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	agent := &AgentExpr{Name: "planner", Service: service}
	origin := &ToolsetExpr{
		Name: "shared-tools",
		Tools: []*ToolExpr{
			{
				Name:        "search",
				Description: "Search docs",
				Args:        &goaexpr.AttributeExpr{Type: goaexpr.String},
			},
			{Name: "summarize", Description: "Summarize docs"},
		},
	}
	used := &ToolsetExpr{
		Name:           "shared-tools",
		Agent:          agent,
		Origin:         origin,
		ToolSelections: []string{"search"},
	}
	agent.Used = &ToolsetGroupExpr{
		Agent:    agent,
		Toolsets: []*ToolsetExpr{used},
	}
	root := &RootExpr{Agents: []*AgentExpr{agent}, Toolsets: []*ToolsetExpr{origin}}

	root.Prepare()

	require.Len(t, used.Tools, 1)
	require.Equal(t, "search", used.Tools[0].Name)
	require.Equal(t, "Search docs", used.Tools[0].Description)
	require.NotSame(t, origin.Tools[0], used.Tools[0])
	require.Equal(t, used, used.Tools[0].Toolset)
}

func TestRootExprValidateRejectsReferencedToolsetOverlayDuplicate(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	agent := &AgentExpr{Name: "planner", Service: service}
	origin := &ToolsetExpr{
		Name: "shared-tools",
		Tools: []*ToolExpr{
			{Name: "ping", Description: "Origin ping"},
			{Name: "pong", Description: "Origin pong"},
		},
	}
	used := &ToolsetExpr{
		Name:   "shared-tools",
		Agent:  agent,
		Origin: origin,
		Tools:  []*ToolExpr{{Name: "ping", Description: "Overlay ping"}},
	}
	agent.Used = &ToolsetGroupExpr{Agent: agent, Toolsets: []*ToolsetExpr{used}}
	root := &RootExpr{Agents: []*AgentExpr{agent}, Toolsets: []*ToolsetExpr{origin}}

	root.Prepare()
	err := root.Validate()

	require.Error(t, err)
	require.ErrorContains(t, err, `tool name "ping" duplicates a tool declared in tool "ping"`)
}

func TestRootExprPrepareSyncsOriginRegistryVersion(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	agent := &AgentExpr{Name: "planner", Service: service}
	origin := &ToolsetExpr{
		Name: "shared-tools",
		Provider: &ProviderExpr{
			Kind:        ProviderRegistry,
			Registry:    &RegistryExpr{Name: "corp"},
			ToolsetName: "shared-tools",
			Version:     "1.2.3",
		},
		version: "1.2.3",
	}
	used := &ToolsetExpr{
		Name:   "shared-tools",
		Agent:  agent,
		Origin: origin,
	}
	agent.Used = &ToolsetGroupExpr{
		Agent:    agent,
		Toolsets: []*ToolsetExpr{used},
	}
	root := &RootExpr{Agents: []*AgentExpr{agent}, Toolsets: []*ToolsetExpr{origin}}

	root.Prepare()

	require.NotNil(t, used.Provider)
	require.Equal(t, ProviderRegistry, used.Provider.Kind)
	require.Equal(t, "1.2.3", used.Provider.Version)
}

func TestRootExprPrepareInheritsOriginToolsetMetadata(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	agent := &AgentExpr{Name: "planner", Service: service}
	origin := &ToolsetExpr{
		Name:        "shared-tools",
		Description: "Shared tools",
		Tags:        []string{"origin"},
		Meta:        goaexpr.MetaExpr{"origin": []string{"yes"}},
		Tools:       []*ToolExpr{{Name: "search"}},
	}
	used := &ToolsetExpr{
		Name:   "shared-tools",
		Agent:  agent,
		Origin: origin,
	}
	agent.Used = &ToolsetGroupExpr{
		Agent:    agent,
		Toolsets: []*ToolsetExpr{used},
	}
	root := &RootExpr{Agents: []*AgentExpr{agent}, Toolsets: []*ToolsetExpr{origin}}

	root.Prepare()

	require.Equal(t, "Shared tools", used.Description)
	require.Equal(t, []string{"origin"}, used.Tags)
	require.Equal(t, goaexpr.MetaExpr{"origin": []string{"yes"}}, used.Meta)
}

func TestRootExprPreparePreservesOverlayToolsetMetadata(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	agent := &AgentExpr{Name: "planner", Service: service}
	origin := &ToolsetExpr{
		Name:        "shared-tools",
		Description: "Shared tools",
		Tags:        []string{"origin"},
		Meta:        goaexpr.MetaExpr{"origin": []string{"yes"}},
		Tools:       []*ToolExpr{{Name: "search"}},
	}
	used := &ToolsetExpr{
		Name:        "shared-tools",
		Description: "Overlay tools",
		Tags:        []string{"overlay"},
		Meta:        goaexpr.MetaExpr{"overlay": []string{"yes"}},
		Agent:       agent,
		Origin:      origin,
	}
	agent.Used = &ToolsetGroupExpr{
		Agent:    agent,
		Toolsets: []*ToolsetExpr{used},
	}
	root := &RootExpr{Agents: []*AgentExpr{agent}, Toolsets: []*ToolsetExpr{origin}}

	root.Prepare()

	require.Equal(t, "Overlay tools", used.Description)
	require.Equal(t, []string{"origin", "overlay"}, used.Tags)
	require.Equal(t, goaexpr.MetaExpr{
		"origin":  []string{"yes"},
		"overlay": []string{"yes"},
	}, used.Meta)
}

func TestRootExprWalkSetsRunsRunPolicyValidation(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	agent := &AgentExpr{Name: "planner", Service: service}
	agent.RunPolicy = &RunPolicyExpr{
		Agent:             agent,
		OnMissingFields:   "await_clarification",
		InterruptsAllowed: false,
	}
	root := &RootExpr{Agents: []*AgentExpr{agent}}

	err := validateWalkedExpressions(root)

	require.ErrorContains(t, err, `OnMissingFields("await_clarification") requires InterruptsAllowed(true)`)
}

func TestRootExprWalkSetsRunsWorkflowValidation(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	agent := &AgentExpr{Name: "planner", Service: service}
	agent.Workflow = &WorkflowExpr{
		Agent: agent,
		GraphNodes: []*WorkflowNodeExpr{
			{ID: "retry", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Tool: "worker.retry", Payload: `{}`}},
		},
	}
	root := &RootExpr{Agents: []*AgentExpr{agent}}

	err := validateWalkedExpressions(root)

	require.ErrorContains(t, err, "Loop requires MaxIterations")
}

type testValidator interface {
	Validate() error
}

func validateWalkedExpressions(root *RootExpr) error {
	verr := new(eval.ValidationErrors)
	root.WalkSets(func(set eval.ExpressionSet) {
		for _, expr := range set {
			validator, ok := expr.(testValidator)
			if !ok {
				continue
			}
			mergeTestValidationError(verr, validator.Validate())
		}
	})
	if len(verr.Errors) == 0 {
		return nil
	}
	return verr
}

func mergeTestValidationError(dst *eval.ValidationErrors, err error) {
	if err == nil {
		return
	}
	var ve *eval.ValidationErrors
	if errors.As(err, &ve) {
		dst.Merge(ve)
	}
}

func TestRootExprValidateRejectsSanitizedAgentCollisionsWithinService(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	root := &RootExpr{
		Agents: []*AgentExpr{
			{Name: "remote-tools", Service: service},
			{Name: "remote_tools", Service: service},
		},
	}

	err := root.Validate()

	require.Error(t, err)
	require.ErrorContains(t, err, `sanitized agent name "remote_tools"`)
}

func TestRootExprValidateRejectsSanitizedToolsetCollisionsWithinOwner(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	agent := &AgentExpr{Name: "planner", Service: service}
	toolsetA := &ToolsetExpr{Name: "remote-tools", Agent: agent}
	toolsetB := &ToolsetExpr{Name: "remote_tools", Agent: agent}
	agent.Used = &ToolsetGroupExpr{
		Agent:    agent,
		Toolsets: []*ToolsetExpr{toolsetA, toolsetB},
	}
	root := &RootExpr{Agents: []*AgentExpr{agent}}

	err := root.Validate()

	require.Error(t, err)
	require.ErrorContains(t, err, `sanitized toolset name "remote_tools"`)
}

func TestRootExprValidateRejectsSanitizedReferencedToolsetCollisionsWithinOwner(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	agent := &AgentExpr{Name: "planner", Service: service}
	originA := &ToolsetExpr{Name: "provider-a"}
	originB := &ToolsetExpr{Name: "provider-b"}
	toolsetA := &ToolsetExpr{Name: "remote-tools", Agent: agent, Origin: originA}
	toolsetB := &ToolsetExpr{Name: "remote_tools", Agent: agent, Origin: originB}
	agent.Used = &ToolsetGroupExpr{
		Agent:    agent,
		Toolsets: []*ToolsetExpr{toolsetA, toolsetB},
	}
	root := &RootExpr{Agents: []*AgentExpr{agent}}

	err := root.Validate()

	require.Error(t, err)
	require.ErrorContains(t, err, `sanitized toolset name "remote_tools"`)
}

func TestRootExprValidateRejectsOwnerScopedDefiningToolsetCollisions(t *testing.T) {
	service := &goaexpr.ServiceExpr{Name: "assistant"}
	planner := &AgentExpr{Name: "planner", Service: service}
	runner := &AgentExpr{Name: "runner", Service: service}
	plannerToolset := &ToolsetExpr{Name: "remote-tools", Agent: planner}
	runnerToolset := &ToolsetExpr{Name: "remote_tools", Agent: runner}
	planner.Used = &ToolsetGroupExpr{
		Agent:    planner,
		Toolsets: []*ToolsetExpr{plannerToolset},
	}
	runner.Used = &ToolsetGroupExpr{
		Agent:    runner,
		Toolsets: []*ToolsetExpr{runnerToolset},
	}
	root := &RootExpr{Agents: []*AgentExpr{planner, runner}}

	err := root.Validate()

	require.Error(t, err)
	require.ErrorContains(t, err, `sanitized toolset name "remote_tools"`)
	require.ErrorContains(t, err, "owner-scoped")
}
