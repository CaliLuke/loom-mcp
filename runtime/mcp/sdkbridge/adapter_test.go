package sdkbridge

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	mcpskills "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/skills"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

type dispatchPayload struct {
	Name string
}

type dispatchResult struct {
	Value string
}

func TestWrapTypedHandlerPreservesTypesAndInterceptorOrder(t *testing.T) {
	var calls []string
	info := NewToolCallInfo("assistant", "summarize", &dispatchPayload{Name: "summarize"}, jsontext.Value(`{"text":"hi"}`))
	interceptors := []TypedInterceptor[*dispatchPayload, *dispatchResult]{
		func(ctx context.Context, got ToolCallInterceptorInfo, payload *dispatchPayload, next TypedHandler[*dispatchPayload, *dispatchResult]) (*dispatchResult, error) {
			calls = append(calls, "first-before")
			assert.Equal(t, "assistant", got.Service())
			assert.Equal(t, "summarize", got.Tool())
			result, err := next(ctx, payload)
			calls = append(calls, "first-after")
			return result, err
		},
		nil,
		func(ctx context.Context, _ ToolCallInterceptorInfo, payload *dispatchPayload, next TypedHandler[*dispatchPayload, *dispatchResult]) (*dispatchResult, error) {
			calls = append(calls, "second-before")
			result, err := next(ctx, payload)
			calls = append(calls, "second-after")
			return result, err
		},
	}
	handler := WrapTypedHandler(interceptors, info, func(_ context.Context, payload *dispatchPayload) (*dispatchResult, error) {
		calls = append(calls, "handler")
		return &dispatchResult{Value: payload.Name}, nil
	})

	result, err := handler(context.Background(), &dispatchPayload{Name: "typed"})

	require.NoError(t, err)
	assert.Equal(t, "typed", result.Value)
	assert.Equal(t, []string{"first-before", "second-before", "handler", "second-after", "first-after"}, calls)
}

func TestDispatchNamedOwnsCommonPromptOrchestration(t *testing.T) {
	var events []string
	mapped := errors.New("mapped prompt failure")
	config := NamedDispatchConfig[*dispatchPayload, *dispatchResult]{
		Method:      "prompts/get",
		Initialized: func(context.Context) bool { return true },
		Name: func(payload *dispatchPayload) string {
			if payload == nil {
				return ""
			}
			return payload.Name
		},
		Operations: []NamedOperation[*dispatchPayload, *dispatchResult]{
			{
				Name: "known",
				Handle: func(context.Context, *dispatchPayload) (*dispatchResult, error) {
					return nil, errors.New("private prompt failure")
				},
			},
		},
		Log:            func(_ context.Context, event string, _ any) { events = append(events, event) },
		MapError:       func(error, string, string) error { return mapped },
		FailureCode:    "internal_error",
		FailureMessage: "Prompt retrieval failed.",
		MissingName:    "Missing prompt name",
		UnknownName:    "Unknown prompt: %s",
	}

	result, err := DispatchNamed(context.Background(), &dispatchPayload{Name: "known"}, config)

	assert.Nil(t, result)
	require.ErrorIs(t, err, mapped)
	assert.Equal(t, []string{"request"}, events)

	_, err = DispatchNamed(context.Background(), &dispatchPayload{Name: "missing"}, config)
	require.ErrorContains(t, err, "Unknown prompt: missing")

	_, err = DispatchNamed(context.Background(), nil, config)
	require.ErrorContains(t, err, "Missing prompt name")
}

func TestDispatchResourceAppliesPolicyBeforeTypedDescriptor(t *testing.T) {
	called := false
	config := ResourceDispatchConfig[*dispatchPayload, *dispatchResult]{
		Initialized: func(context.Context) bool { return true },
		URI: func(payload *dispatchPayload) string {
			if payload == nil {
				return ""
			}
			return payload.Name
		},
		Policy: ResourcePolicy{
			AllowedURIs:       []string{"doc://"},
			ResourceNameToURI: map[string]string{"documents": "doc://"},
		},
		Resources: []ResourceOperation[*dispatchPayload, *dispatchResult]{
			{
				URI: "doc://list",
				Handle: func(_ context.Context, _ *dispatchPayload, baseURI string) (*dispatchResult, error) {
					called = true
					return &dispatchResult{Value: baseURI}, nil
				},
			},
		},
	}

	result, err := DispatchResource(context.Background(), &dispatchPayload{Name: "doc://list?page=2"}, config)

	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "doc://list", result.Value)

	called = false
	config.Policy.DeniedURIs = []string{"doc://list"}
	_, err = DispatchResource(context.Background(), &dispatchPayload{Name: "doc://list?page=2"}, config)
	require.ErrorContains(t, err, "Resource URI is not allowed")
	assert.False(t, called)
}

func TestResourcePolicyCombinesServerAndRequestNameGrants(t *testing.T) {
	policy := ResourcePolicy{
		AllowedNames:      []string{"documents"},
		DeniedURIs:        []string{"doc://private"},
		ResourceNameToURI: map[string]string{"documents": "doc://", "private": "doc://private"},
	}
	ctx := mcpruntime.WithAllowedResourceNames(context.Background(), "documents")

	require.NoError(t, policy.Authorize(ctx, "doc://list?page=1", nil))
	require.ErrorContains(t, policy.Authorize(ctx, "other://list", nil), "not allowed")

	ctx = mcpruntime.WithDeniedResourceNames(ctx, "private")
	require.ErrorContains(t, policy.Authorize(ctx, "doc://private", nil), "denied")
}

func TestToolCallInfoExposesTypedMetadata(t *testing.T) {
	payload := &dispatchPayload{Name: "summarize"}
	raw := jsontext.Value(`{"text":"hi"}`)
	info := NewToolCallInfo("assistant", "summarize", payload, raw)

	assert.Equal(t, "assistant", info.Service())
	assert.Equal(t, "tools/call", info.Method())
	assert.Equal(t, "summarize", info.Tool())
	assert.Equal(t, payload, info.RawPayload())
	assert.Equal(t, raw, info.RawArguments())
	assert.Equal(t, loom.InterceptorUnary, info.CallType())
}

func TestResourceQueryJSONCoercesURIValues(t *testing.T) {
	encoded, err := ResourceQueryJSON("doc://list?count=2&enabled=true&tag=a&tag=b")
	require.NoError(t, err)
	require.JSONEq(t, `{"count":2,"enabled":true,"tag":["a","b"]}`, string(encoded))

	typed, err := ResourceQueryJSONTyped(
		"doc://list?query=true&tags=one&nums=2",
		map[string]mcpruntime.QueryField{
			"query": {String: true},
			"tags":  {String: true, Repeated: true},
			"nums":  {Repeated: true},
		},
		"",
	)
	require.NoError(t, err)
	require.JSONEq(t, `{"query":"true","tags":["one"],"nums":[2]}`, string(typed))
	empty, err := ResourceQueryJSON("doc://list")
	require.NoError(t, err)
	assert.JSONEq(t, "{}", string(empty))
	_, err = ResourceQueryJSON("doc://%gh")
	require.ErrorContains(t, err, "invalid resource URI")
}

func TestResourceURIMatcherRejectsUndeclaredQueryShapes(t *testing.T) {
	matcher := ResourceURIMatcher{
		Pattern: regexp.MustCompile(`^doc://list(?:\?.*)?$`),
		QueryFields: map[string]ResourceQueryField{
			"count": {},
			"tags":  {Repeated: true},
		},
	}
	for _, uri := range []string{
		"doc://list",
		"doc://list?count=2",
		"doc://list?tags=a&tags=b",
	} {
		assert.True(t, matcher.Match(uri), uri)
	}
	for _, uri := range []string{
		"doc://other?count=2",
		"doc://list?extra=1",
		"doc://list?count=1&count=2",
		"doc://list?count=1;extra=2",
		"doc://list?count",
		"doc://list?count=1&&tags=a",
		"doc://list?",
	} {
		assert.False(t, matcher.Match(uri), uri)
	}
}
func TestResourceURIMatcherRequiresAFullPatternMatch(t *testing.T) {
	matcher := ResourceURIMatcher{Pattern: regexp.MustCompile("doc://list")}

	assert.True(t, matcher.Match("doc://list"))
	assert.False(t, matcher.Match("prefix-doc://list"))
	assert.False(t, matcher.Match("doc://list-suffix"))
}
func TestResourceURIMatcherEnforcesGeneratedQuerySchema(t *testing.T) {
	const schema = `{"type":"object","properties":{"count":{"type":"integer","minimum":2},"scope":{"type":"string","pattern":"^prod$"}},"required":["count","scope"],"additionalProperties":false}`
	matcher := ResourceURIMatcher{
		Pattern:     regexp.MustCompile(`^doc://list(?:\?.*)?$`),
		QueryFields: map[string]ResourceQueryField{"count": {}, "scope": {String: true}},
		QuerySchema: schema,
	}

	assert.True(t, matcher.Match("doc://list?count=2&scope=prod"))
	assert.False(t, matcher.Match("doc://list?count=2"))
	assert.False(t, matcher.Match("doc://list?count=1&scope=prod"))
	assert.False(t, matcher.Match("doc://list?count=2&scope=staging"))
}

func TestResourceQueryJSONTypedPreservesExactBoundsAndDefaults(t *testing.T) {
	const schema = `{"type":"object","properties":{"n":{"type":"integer","maximum":9007199254740992},"limit":{"type":"integer","minimum":1}},"required":["n","limit"],"additionalProperties":false}`
	fields := map[string]mcpruntime.QueryField{
		"n":     {Unsigned: true, Bits: 64},
		"limit": {Bits: 32, DefaultValues: []string{"25"}},
	}

	encoded, err := ResourceQueryJSONTyped("urn:test?n=9007199254740992", fields, schema)
	require.NoError(t, err)
	require.JSONEq(t, `{"n":9007199254740992,"limit":25}`, string(encoded))

	_, err = ResourceQueryJSONTyped("urn:test?n=9007199254740993", fields, schema)
	require.Error(t, err)
	_, err = ResourceQueryJSONTyped("urn:test?n=1&limit=2147483648", fields, schema)
	require.Error(t, err)
}
func TestResourceQueryJSONTypedAcceptsIntegralFloatLexemes(t *testing.T) {
	const schema = `{"type":"object","properties":{"ratio":{"type":"number"},"ratios":{"type":"array","items":{"type":"number"}}},"required":["ratio","ratios"],"additionalProperties":false}`
	fields := map[string]mcpruntime.QueryField{
		"ratio":  {Float: true},
		"ratios": {Float: true, Repeated: true},
	}

	encoded, err := ResourceQueryJSONTyped("urn:test?ratio=9223372036854775808&ratios=9223372036854775808&ratios=1", fields, schema)
	require.NoError(t, err)
	var decoded struct {
		Ratio  float64   `json:"ratio"`
		Ratios []float64 `json:"ratios"`
	}
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.InDelta(t, float64(9223372036854775808), decoded.Ratio, 0)
	require.Len(t, decoded.Ratios, 2)
	assert.InDelta(t, float64(9223372036854775808), decoded.Ratios[0], 0)
	assert.InDelta(t, 1, decoded.Ratios[1], 0)
}

func TestResourceQueryJSONTypedPreservesFloat32SchemaValues(t *testing.T) {
	const schema = `{"type":"object","properties":{"ratio":{"type":"number","enum":[1.2]},"ratios":{"type":"array","items":{"type":"number","maximum":1.2}},"fallback":{"type":"number","enum":[1.2]}},"required":["ratio","ratios","fallback"],"additionalProperties":false}`
	fields := map[string]mcpruntime.QueryField{
		"ratio":    {Float: true, Bits: 32},
		"ratios":   {Float: true, Bits: 32, Repeated: true},
		"fallback": {Float: true, Bits: 32, DefaultValues: []string{"1.2"}},
	}

	encoded, err := ResourceQueryJSONTyped("urn:test?ratio=1.2&ratios=1.2&ratios=1.1", fields, schema)
	require.NoError(t, err)
	require.JSONEq(t, `{"ratio":1.2,"ratios":[1.2,1.1],"fallback":1.2}`, string(encoded))
}
func TestDecodeMetaPreservesExactNumbersAndRejectsInvalidValues(t *testing.T) {
	meta, err := DecodeMeta(loom.JSONValue(`{"id":9007199254740993}`))
	require.NoError(t, err)
	encoded, err := json.Marshal(meta)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":9007199254740993}`, string(encoded))

	empty, err := DecodeMeta(loom.JSONValue(nil))
	require.NoError(t, err)
	assert.Nil(t, empty)
	_, err = DecodeMeta(jsontext.Value(`{"id":`))
	require.Error(t, err)
	_, err = DecodeMeta(jsontext.Value(`[]`))
	require.Error(t, err)
}

func TestDispatchNamedLogsSuccessfulTypedOperation(t *testing.T) {
	var events []string
	result, err := DispatchNamed(context.Background(), &dispatchPayload{Name: "known"}, NamedDispatchConfig[*dispatchPayload, *dispatchResult]{
		Method:      "prompts/get",
		Initialized: func(context.Context) bool { return true },
		Name:        func(payload *dispatchPayload) string { return payload.Name },
		Operations: []NamedOperation[*dispatchPayload, *dispatchResult]{{
			Name: "known",
			Handle: func(context.Context, *dispatchPayload) (*dispatchResult, error) {
				return &dispatchResult{Value: "typed"}, nil
			},
		}},
		Log: func(_ context.Context, event string, _ any) { events = append(events, event) },
	})

	require.NoError(t, err)
	assert.Equal(t, "typed", result.Value)
	assert.Equal(t, []string{"request", "response"}, events)
}

func TestDispatchResourceOwnsSkillRouting(t *testing.T) {
	root := t.TempDir()
	var skillErrorDetails map[string]any
	directory := filepath.Join(root, "code-review")
	require.NoError(t, os.Mkdir(directory, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("# Code Review\nReview code.\n"), 0o600))

	config := ResourceDispatchConfig[*dispatchPayload, *dispatchResult]{
		Initialized:  func(context.Context) bool { return true },
		URI:          func(payload *dispatchPayload) string { return payload.Name },
		SkillSources: []mcpskills.Source{{Root: root}},
		Log: func(_ context.Context, event string, details any) {
			if event == "error" {
				skillErrorDetails, _ = details.(map[string]any)
			}
		},
		SkillResult: func(content *mcpskills.Content) *dispatchResult {
			return &dispatchResult{Value: *content.Text}
		},
	}
	result, err := DispatchResource(context.Background(), &dispatchPayload{Name: "skill://code-review/SKILL.md"}, config)
	require.NoError(t, err)
	assert.Contains(t, result.Value, "# Code Review")

	_, err = DispatchResource(context.Background(), &dispatchPayload{Name: "skill://missing/SKILL.md"}, config)
	require.ErrorContains(t, err, "Unable to read skill resource")
	assert.Equal(t, "resources/read", skillErrorDetails["method"])
	assert.Equal(t, "skill://missing/SKILL.md", skillErrorDetails["uri"])
	assert.NotEmpty(t, skillErrorDetails["error"])
}

func TestDispatchRejectsInvalidDescriptorState(t *testing.T) {
	_, err := DispatchNamed(context.Background(), &dispatchPayload{Name: "known"}, NamedDispatchConfig[*dispatchPayload, *dispatchResult]{
		Initialized: func(context.Context) bool { return false },
		Name:        func(payload *dispatchPayload) string { return payload.Name },
	})
	require.ErrorContains(t, err, "Not initialized")

	_, err = DispatchResource(context.Background(), nil, ResourceDispatchConfig[*dispatchPayload, *dispatchResult]{
		Initialized: func(context.Context) bool { return true },
		URI: func(payload *dispatchPayload) string {
			if payload == nil {
				return ""
			}
			return payload.Name
		},
	})
	require.ErrorContains(t, err, "Missing resource URI")

	_, err = DispatchResource(context.Background(), &dispatchPayload{Name: "doc://list"}, ResourceDispatchConfig[*dispatchPayload, *dispatchResult]{
		Initialized: func(context.Context) bool { return true },
		URI:         func(payload *dispatchPayload) string { return payload.Name },
		Resources:   []ResourceOperation[*dispatchPayload, *dispatchResult]{{URI: "doc://list"}},
		MapError:    func(err error, _, _ string) error { return err },
	})
	require.ErrorContains(t, err, "has no handler")
}

func TestInvalidClientInputPreservesClassifiedError(t *testing.T) {
	sentinel := loom.PermanentError("invalid_params", "Missing required argument: context")
	err := InvalidClientInput(sentinel)

	require.ErrorIs(t, err, sentinel)
	assert.True(t, mcpruntime.IsInvalidClientInput(err))
}
