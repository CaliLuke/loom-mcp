package statefulevaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	assistantapi "example.com/assistant"
	"example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type evaluationFixture struct {
	state           discoveryState
	generation      string
	compact, native agentsruntime.ToolsetRegistration
	catalog         []descriptor
	cases           []retrievalCase
	denied          map[discoveryKey]bool
	service         *countedService
	sequence        int
}
type retrievalCase struct {
	ID     string `json:"id"`
	Query  string `json:"query"`
	Routes []struct {
		Tool         string   `json:"tool"`
		InputFields  []string `json:"input_fields"`
		OutputFields []string `json:"output_fields"`
	} `json:"routes"`
	UnsupportedOutputFields []string `json:"unsupported_output_fields"`
}
type countedService struct {
	assistant.Service
	summaries int
}
type authorityKey struct{}
type invocation struct {
	Stage              string          `json:"stage"`
	Failure            string          `json:"failure,omitzero"`
	Payload            rawjson.Message `json:"payload,omitzero"`
	RequestBytes       int             `json:"request_bytes"`
	SetupNS, ExecuteNS int64
}
type discoveryResult struct {
	Names        []string
	Descriptors  []descriptor
	Search       invocation
	PayloadBytes int
	Rejected     []string
}

const catalogHash = "5533903d33ecbb42f6523b353ba9bfb8c60c0a705ee499c9b28223736a66a94b"
const casesHash = "a248254ef490c931f2284648a1c97f5a796b6b368f752c1081247ccbd020a62b"

func (s *countedService) SummarizeText(ctx context.Context, p *assistant.SummarizeTextPayload) (*assistant.SummarizeTextResult, error) {
	s.summaries++
	return s.Service.SummarizeText(ctx, p)
}

func newEvaluationFixture(t *testing.T) *evaluationFixture {
	t.Helper()
	root, err := os.OpenRoot("../../discoverybench/testdata")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, root.Close())
	})
	catalogData, err := root.ReadFile("catalog.json")
	require.NoError(t, err)
	require.Equal(t, catalogHash, fmt.Sprintf("%x", sha256.Sum256(catalogData)))
	casesData, err := root.ReadFile("cases.json")
	require.NoError(t, err)
	require.Equal(t, casesHash, fmt.Sprintf("%x", sha256.Sum256(casesData)))
	var catalog struct {
		Tools []descriptor `json:"tools"`
	}
	var cases struct {
		Cases []retrievalCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(catalogData, &catalog))
	require.NoError(t, json.Unmarshal(casesData, &cases))
	require.Len(t, catalog.Tools, 15)
	require.Len(t, cases.Cases, 12)
	f := &evaluationFixture{state: newDiscoveryState(), generation: catalogHash, catalog: catalog.Tools, cases: cases.Cases, denied: make(map[discoveryKey]bool), service: &countedService{Service: assistantapi.NewAssistant()}}
	authority := func(ctx context.Context, info mcpassistant.ToolCallInterceptorInfo, payload *mcpassistant.ToolsCallPayload, next mcpassistant.ToolCallHandler) (*mcpassistant.ToolsCallResult, error) {
		key, ok := ctx.Value(authorityKey{}).(discoveryKey)
		if !ok {
			return nil, errors.New("missing host authority scope")
		}
		if info.Tool() == "summarize_text" && f.denied[key] {
			return nil, errors.New("authority denied")
		}
		return next(ctx, payload)
	}
	makeRegistration := func(pins []string) agentsruntime.ToolsetRegistration {
		adapter := mcpassistant.NewMCPAdapter(f.service, nil, &mcpassistant.MCPAdapterOptions{ToolSearch: &mcpassistant.ToolSearchOptions{AlwaysVisible: pins, MaxResults: 5}, ToolCallInterceptors: []mcpassistant.ToolCallInterceptor{authority}})
		registration, err := mcpassistant.NewAssistantAssistantMcpLocalToolsetRegistration(adapter)
		require.NoError(t, err)
		return registration
	}
	f.compact = makeRegistration([]string{"search"})
	names := make([]string, 0, len(catalog.Tools))
	for _, d := range catalog.Tools {
		names = append(names, d.Name)
	}
	f.native = makeRegistration(names)
	for _, d := range catalog.Tools {
		spec := f.spec(t, d.Name)
		if d.Name == "report_progress" {
			assert.JSONEq(t, `{"type":"object"}`, string(d.InputSchema))
			assert.JSONEq(t, `{"type":"object","properties":{},"additionalProperties":false}`, string(spec.Payload.Schema))
		} else {
			assert.JSONEq(t, string(d.InputSchema), string(spec.Payload.Schema), d.Name)
		}
		assert.JSONEq(t, string(d.OutputSchema), string(spec.Result.Schema))
	}
	return f
}

// registration projects visibility only; execution and validation remain generated.
func (f *evaluationFixture) registration(arm string, key discoveryKey) agentsruntime.ToolsetRegistration {
	if arm == "compact" {
		return f.compact
	}
	selected := f.state.names(key, f.generation)
	registration := f.native
	registration.Specs = nil
	for _, spec := range f.native.Specs {
		name := string(spec.Name)
		if name == "search_tools" || name == "search" || slices.Contains(selected, name) {
			registration.Specs = append(registration.Specs, spec)
		}
	}
	return registration
}

func (f *evaluationFixture) spec(t *testing.T, name string) tools.ToolSpec {
	t.Helper()
	for _, spec := range f.native.Specs {
		if string(spec.Name) == name {
			return spec
		}
	}
	t.Fatalf("catalog tool %q missing from generated native specs", name)
	return tools.ToolSpec{}
}

// invoke separates actual unknown-name routing from generated executor success.
// Runtime materialization is intentionally covered by a distinct limitation probe.
func (f *evaluationFixture) invoke(t *testing.T, arm string, key discoveryKey, name string, args any) invocation {
	t.Helper()
	requestName := name
	requestArgs := args
	if arm == "compact" && name != "search_tools" && name != "search" {
		requestName = "call_tool"
		requestArgs = map[string]any{"name": name, "arguments": args}
	}
	payload, err := json.Marshal(requestArgs, json.Deterministic(true))
	require.NoError(t, err)
	setup := time.Now()
	rt := agentsruntime.New()
	registration := f.registration(arm, key)
	require.NoError(t, rt.RegisterToolset(registration))
	result := invocation{RequestBytes: len(payload) + len(requestName), SetupNS: time.Since(setup).Nanoseconds()}
	f.sequence++
	ctx := context.WithValue(t.Context(), authorityKey{}, key)
	input := &agentsruntime.ToolInput{AgentID: agent.Ident(key.Agent), RunID: "evaluation-run", RootRunID: "evaluation-run", SessionID: key.Session, ToolName: tools.Ident(requestName), ToolCallID: fmt.Sprintf("call-%d", f.sequence), Payload: payload}
	start := time.Now()
	if !slices.ContainsFunc(registration.Specs, func(spec tools.ToolSpec) bool {
		return spec.Name == input.ToolName
	}) {
		result.Stage = "runtime_routing_rejection"
		_, err := rt.ExecuteToolActivity(ctx, input)
		result.ExecuteNS = time.Since(start).Nanoseconds()
		require.Error(t, err)
		result.Failure = err.Error()
		return result
	}
	result.Stage = "generated_executor"
	execution, err := registration.Execute(ctx, &planner.ToolRequest{Name: input.ToolName, AgentID: input.AgentID, RunID: input.RunID, RootRunID: input.RootRunID, SessionID: input.SessionID, ToolCallID: input.ToolCallID, Payload: input.Payload})
	result.ExecuteNS = time.Since(start).Nanoseconds()
	if err != nil {
		result.Failure = err.Error()
		return result
	}
	require.NotNil(t, execution)
	require.NotNil(t, execution.ToolResult)
	if execution.ToolResult.Error != nil {
		result.Failure = execution.ToolResult.Error.Error()
		return result
	}
	result.Payload, err = json.Marshal(execution.ToolResult.Result, json.Deterministic(true))
	require.NoError(t, err)
	return result
}

func (f *evaluationFixture) discover(t *testing.T, key discoveryKey, query string) discoveryResult {
	t.Helper()
	response := f.invoke(t, "compact", key, "search_tools", map[string]any{"query": query, "max_results": 5, "include_schemas": true})
	require.Empty(t, response.Failure)
	var found struct {
		Tools []descriptor `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(response.Payload, &found))
	result := discoveryResult{PayloadBytes: len(response.Payload), Descriptors: found.Tools, Search: response}
	for _, d := range found.Tools {
		result.Names = append(result.Names, d.Name)
		if d.Name == "search" {
			continue
		}
		err := f.state.reveal(key, f.generation, d)
		if errors.Is(err, errDescriptorTooLarge) {
			result.Rejected = append(result.Rejected, d.Name)
			continue
		}
		require.NoError(t, err)
	}
	return result
}

func modelTools(t *testing.T, specs []tools.ToolSpec) []*model.ToolDefinition {
	t.Helper()
	definitions := make([]*model.ToolDefinition, 0, len(specs))
	for _, spec := range specs {
		schema := jsontext.Value(bytes.Clone(spec.Payload.Schema))
		definitions = append(definitions, &model.ToolDefinition{Name: string(spec.Name), Description: spec.Description, InputSchema: schema})
	}
	return definitions
}
