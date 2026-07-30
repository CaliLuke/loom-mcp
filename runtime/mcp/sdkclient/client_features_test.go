package sdkclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testRequestStateKey = []byte("0123456789abcdef0123456789abcdef")

func TestWithClientFeaturesIgnoresNilSession(t *testing.T) {
	t.Parallel()

	ctx := WithClientFeatures(context.Background(), nil, ClientFeaturesOptions{})

	_, hasElicitor := mcpruntime.ElicitorFromContext(ctx)
	_, hasProgressReporter := mcpruntime.ProgressReporterFromContext(ctx)
	require.False(t, hasElicitor)
	require.False(t, hasProgressReporter)
}

func TestClientFeatureAdapterUsesMultiRoundTripElicitation(t *testing.T) {
	t.Parallel()

	session := &mcp.ServerSession{}
	projectRequest := mcpruntime.ElicitRequest{
		ElicitationID:   "elicit-project",
		Message:         "Choose a project",
		Mode:            "form",
		RequestedSchema: map[string]any{"type": "object"},
	}
	regionRequest := mcpruntime.ElicitRequest{
		ElicitationID:   "elicit-region",
		Message:         "Choose a region",
		Mode:            "form",
		RequestedSchema: map[string]any{"type": "object"},
	}

	ctx := WithClientFeatures(context.Background(), session, testClientFeaturesOptions(nil, ""))
	elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
	require.True(t, ok)
	_, err := elicitor.Elicit(ctx, projectRequest)
	requests, state, ok := InputRequired(err)
	require.True(t, ok)
	require.NotEmpty(t, state)
	require.Len(t, requests, 1)
	elicitParams, ok := requests["loom-input-0"].(*mcp.ElicitParams)
	require.True(t, ok)
	assert.Equal(t, "elicit-project", elicitParams.ElicitationID)
	assert.Equal(t, "Choose a project", elicitParams.Message)
	assert.Equal(t, "form", elicitParams.Mode)

	ctx = WithClientFeatures(context.Background(), session, testClientFeaturesOptions(mcp.InputResponseMap{
		"loom-input-0": &mcp.ElicitResult{
			Action:  elicitActionAccept,
			Content: map[string]any{"project": "loom"},
		},
	}, state))
	elicitor, ok = mcpruntime.ElicitorFromContext(ctx)
	require.True(t, ok)
	projectResult, err := elicitor.Elicit(ctx, projectRequest)
	require.NoError(t, err)
	assert.Equal(t, &mcpruntime.ElicitResult{
		Action:  elicitActionAccept,
		Content: map[string]any{"project": "loom"},
	}, projectResult)

	_, err = elicitor.Elicit(ctx, regionRequest)
	requests, state, ok = InputRequired(err)
	require.True(t, ok)
	require.NotEmpty(t, state)
	elicitParams, ok = requests["loom-input-1"].(*mcp.ElicitParams)
	require.True(t, ok)
	assert.Equal(t, "elicit-region", elicitParams.ElicitationID)
	assert.Equal(t, "Choose a region", elicitParams.Message)

	ctx = WithClientFeatures(context.Background(), session, testClientFeaturesOptions(mcp.InputResponseMap{
		"loom-input-1": &mcp.ElicitResult{
			Action:  elicitActionAccept,
			Content: map[string]any{"region": "eu-west"},
		},
	}, state))
	elicitor, ok = mcpruntime.ElicitorFromContext(ctx)
	require.True(t, ok)
	projectResult, err = elicitor.Elicit(ctx, projectRequest)
	require.NoError(t, err)
	regionResult, err := elicitor.Elicit(ctx, regionRequest)
	require.NoError(t, err)
	assert.Equal(t, "loom", projectResult.Content["project"])
	assert.Equal(t, "eu-west", regionResult.Content["region"])
}

func TestClientFeatureAdaptersFailClosedWithoutDependencies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, err := (sessionElicitor{}).Elicit(ctx, mcpruntime.ElicitRequest{})
	require.ErrorIs(t, err, mcpruntime.ErrElicitorUnavailable)
	err = (sessionProgressReporter{}).ReportProgress(ctx, nil, mcpruntime.ProgressUpdate{})
	require.ErrorIs(t, err, mcpruntime.ErrProgressReporterUnavailable)
}

func TestClientFeatureAdapterRejectsInvalidRequestState(t *testing.T) {
	t.Parallel()

	t.Run("malformed", func(t *testing.T) {
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(nil, "not-base64"))
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err := elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
		require.ErrorContains(t, err, "decode MCP requestState")
	})

	t.Run("unsupported version", func(t *testing.T) {
		state, err := encryptRequestState([]byte(`{"version":2}`), testRequestStateKey, testRequestStateAAD(t))
		require.NoError(t, err)
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(nil, state))
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err = elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
		require.ErrorContains(t, err, "unsupported MCP requestState version")
	})

	t.Run("stored response without pending request", func(t *testing.T) {
		state, err := encryptRequestState([]byte(`{"version":1,"responses":{"loom-input-0":{"unexpected":true}}}`), testRequestStateKey, testRequestStateAAD(t))
		require.NoError(t, err)
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(nil, state))
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err = elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
		require.ErrorContains(t, err, `MCP input response "loom-input-0" has no pending request`)
	})

	t.Run("oversized payload", func(t *testing.T) {
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(nil, strings.Repeat("a", maxRequestStateBytes+1)))
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err := elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
		require.ErrorContains(t, err, fmt.Sprintf("payload exceeds %d bytes", maxRequestStateBytes))
	})

	t.Run("tampered", func(t *testing.T) {
		state, err := encryptRequestState([]byte(`{"version":1}`), testRequestStateKey, testRequestStateAAD(t))
		require.NoError(t, err)
		sealed, err := base64.RawURLEncoding.DecodeString(state)
		require.NoError(t, err)
		sealed[len(sealed)-1] ^= 1
		state = base64.RawURLEncoding.EncodeToString(sealed)
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(nil, state))
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err = elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
		require.ErrorContains(t, err, "verify and decrypt payload")
	})

	t.Run("wrong key", func(t *testing.T) {
		state, err := encryptRequestState([]byte(`{"version":1}`), testRequestStateKey, testRequestStateAAD(t))
		require.NoError(t, err)
		wrongKey := []byte("abcdef0123456789abcdef0123456789")
		opts := testClientFeaturesOptions(nil, state)
		opts.RequestStateKey = wrongKey
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, opts)
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err = elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
		require.ErrorContains(t, err, "verify and decrypt payload")
	})

	t.Run("missing key", func(t *testing.T) {
		state, err := encryptRequestState([]byte(`{"version":1}`), testRequestStateKey, testRequestStateAAD(t))
		require.NoError(t, err)
		opts := testClientFeaturesOptions(nil, state)
		opts.RequestStateKey = nil
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, opts)
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err = elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
		require.ErrorContains(t, err, fmt.Sprintf("key must be exactly %d bytes", requestStateKeyBytes))
	})

	t.Run("cross-request replay", func(t *testing.T) {
		state, err := encryptRequestState([]byte(`{"version":1}`), testRequestStateKey, testRequestStateAAD(t))
		require.NoError(t, err)
		opts := testClientFeaturesOptions(nil, state)
		opts.RequestParams = map[string]any{"name": "other-tool"}
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, opts)
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err = elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
		require.ErrorContains(t, err, "verify and decrypt payload")
	})
}

func TestClientFeatureAdapterRejectsResponsesOutsidePendingRound(t *testing.T) {
	t.Parallel()

	firstRequest := mcpruntime.ElicitRequest{Message: "first"}
	ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(nil, ""))
	elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
	require.True(t, ok)
	_, err := elicitor.Elicit(ctx, firstRequest)
	_, state, inputRequired := InputRequired(err)
	require.True(t, inputRequired)

	t.Run("future response", func(t *testing.T) {
		responses := mcp.InputResponseMap{
			"loom-input-0": &mcp.ElicitResult{Action: elicitActionAccept},
			"loom-input-1": &mcp.ElicitResult{Action: elicitActionAccept},
		}
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(responses, state))
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err := elicitor.Elicit(ctx, firstRequest)
		require.ErrorContains(t, err, "does not match pending request count")
		require.True(t, mcpruntime.IsInvalidClientInput(err))
	})

	t.Run("wrong response ID", func(t *testing.T) {
		responses := mcp.InputResponseMap{
			"loom-input-1": &mcp.ElicitResult{Action: elicitActionAccept},
		}
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(responses, state))
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err := elicitor.Elicit(ctx, firstRequest)
		require.ErrorContains(t, err, `missing pending request "loom-input-0"`)
	})

	t.Run("changed request contract", func(t *testing.T) {
		responses := mcp.InputResponseMap{
			"loom-input-0": &mcp.ElicitResult{Action: elicitActionAccept},
		}
		ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(responses, state))
		elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
		require.True(t, ok)
		_, err := elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "changed"})
		require.ErrorContains(t, err, "does not match the pending request")
	})
}

func TestClientFeatureAdapterRejectsInvalidInputResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		responses mcp.InputResponseMap
		wantError string
	}{
		{
			name: "wrong concrete response type",
			responses: mustInputResponses(t, `{
				"loom-input-0": {
					"role": "assistant",
					"content": [{"type": "text", "text": "wrong response type"}],
					"model": "test-model"
				}
			}`),
			wantError: `MCP input response "loom-input-0" has type *mcp.CreateMessageWithToolsResult; want *mcp.ElicitResult`,
		},
		{
			name: "invalid elicitation action",
			responses: mcp.InputResponseMap{
				"loom-input-0": &mcp.ElicitResult{Action: "approve"},
			},
			wantError: `MCP input response "loom-input-0" has invalid elicitation action`,
		},
		{
			name: "decline with content",
			responses: mcp.InputResponseMap{
				"loom-input-0": &mcp.ElicitResult{
					Action:  elicitActionDecline,
					Content: map[string]any{"value": "must not be present"},
				},
			},
			wantError: `MCP input response "loom-input-0" has content for "decline" action`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mcpruntime.ElicitRequest{Message: "continue?"}
			ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(test.responses, testPendingRequestState(t, request)))
			elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
			require.True(t, ok)
			_, err := elicitor.Elicit(ctx, request)
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestClientFeatureAdapterRejectsTooManyInputResponses(t *testing.T) {
	t.Parallel()

	responses := make(mcp.InputResponseMap, maxInputResponses+1)
	for i := range maxInputResponses + 1 {
		responses[fmt.Sprintf("loom-input-%d", i)] = &mcp.ElicitResult{Action: elicitActionCancel}
	}
	ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(responses, testPendingRequestState(t, mcpruntime.ElicitRequest{Message: "continue?"})))
	elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
	require.True(t, ok)
	_, err := elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
	require.ErrorContains(t, err, fmt.Sprintf("MCP input response count exceeds %d", maxInputResponses))
}

func TestClientFeatureAdapterBoundsEmittedRequestState(t *testing.T) {
	t.Parallel()

	maxContentSize := largestAcceptedRequestStateContentSize(t)
	session := &mcp.ServerSession{}
	responses := elicitationResponsesWithContentSize(maxContentSize)
	firstRequest := mcpruntime.ElicitRequest{Message: "first"}
	ctx := WithClientFeatures(context.Background(), session, testClientFeaturesOptions(responses, testPendingRequestState(t, firstRequest)))
	elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
	require.True(t, ok)

	_, err := elicitor.Elicit(ctx, firstRequest)
	require.NoError(t, err)
	_, err = elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "second"})
	_, state, inputRequired := InputRequired(err)
	require.True(t, inputRequired)
	require.LessOrEqual(t, len(state), maxRequestStateBytes)

	ctx = WithClientFeatures(context.Background(), session, testClientFeaturesOptions(elicitationResponsesWithContentSize(maxContentSize+1), testPendingRequestState(t, firstRequest)))
	elicitor, ok = mcpruntime.ElicitorFromContext(ctx)
	require.True(t, ok)
	_, err = elicitor.Elicit(ctx, firstRequest)
	require.NoError(t, err)
	_, err = elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "second"})
	require.ErrorContains(t, err, fmt.Sprintf("encoded MCP requestState exceeds %d bytes", maxRequestStateBytes))
}

func TestInputRequiredImplementsRuntimeMarker(t *testing.T) {
	t.Parallel()

	ctx := WithClientFeatures(context.Background(), &mcp.ServerSession{}, testClientFeaturesOptions(nil, ""))
	elicitor, ok := mcpruntime.ElicitorFromContext(ctx)
	require.True(t, ok)
	_, err := elicitor.Elicit(ctx, mcpruntime.ElicitRequest{Message: "continue?"})
	require.True(t, mcpruntime.IsInputRequired(err))
}

func largestAcceptedRequestStateContentSize(t *testing.T) int {
	t.Helper()

	low, high := 0, maxRequestStateBytes
	for low < high {
		candidate := low + (high-low+1)/2
		responses := elicitationResponsesWithContentSize(candidate)
		data, err := json.Marshal(responses["loom-input-0"])
		require.NoError(t, err)
		requests := testInputRequests(t,
			pendingTestInput{id: "loom-input-0", request: mcpruntime.ElicitRequest{Message: "first"}},
			pendingTestInput{id: "loom-input-1", request: mcpruntime.ElicitRequest{Message: "second"}},
		)
		_, err = encodePersistedRequestState(map[string]json.RawMessage{"loom-input-0": data}, requests, []string{"loom-input-1"}, testRequestStateKey, testRequestStateAAD(t))
		if err == nil {
			low = candidate
		} else {
			high = candidate - 1
		}
	}
	return low
}

func elicitationResponsesWithContentSize(size int) mcp.InputResponseMap {
	return mcp.InputResponseMap{
		"loom-input-0": &mcp.ElicitResult{
			Action:  elicitActionAccept,
			Content: map[string]any{"value": strings.Repeat("a", size)},
		},
	}
}

func testClientFeaturesOptions(responses mcp.InputResponseMap, state string) ClientFeaturesOptions {
	return ClientFeaturesOptions{
		InputResponses:  responses,
		RequestState:    state,
		RequestStateKey: testRequestStateKey,
		RequestMethod:   "tools/call",
		RequestParams:   map[string]any{"name": "test-tool"},
	}
}

func testRequestStateAAD(t *testing.T) []byte {
	t.Helper()
	aad, err := requestStateBinding("tools/call", map[string]any{"name": "test-tool"})
	require.NoError(t, err)
	return aad
}

func testPendingRequestState(t *testing.T, request mcpruntime.ElicitRequest) string {
	t.Helper()
	state, err := encodePersistedRequestState(nil, testInputRequests(t, pendingTestInput{id: "loom-input-0", request: request}), []string{"loom-input-0"}, testRequestStateKey, testRequestStateAAD(t))
	require.NoError(t, err)
	return state
}

type pendingTestInput struct {
	id      string
	request mcpruntime.ElicitRequest
}

func testInputRequests(t *testing.T, inputs ...pendingTestInput) map[string]persistedInputRequest {
	t.Helper()
	requests := make(map[string]persistedInputRequest, len(inputs))
	for _, input := range inputs {
		persisted, err := persistInputRequest(&mcp.ElicitParams{
			ElicitationID:   input.request.ElicitationID,
			Message:         input.request.Message,
			Mode:            input.request.Mode,
			RequestedSchema: input.request.RequestedSchema,
			URL:             input.request.URL,
		})
		require.NoError(t, err)
		requests[input.id] = persisted
	}
	return requests
}

func mustInputResponses(t *testing.T, data string) mcp.InputResponseMap {
	t.Helper()
	var responses mcp.InputResponseMap
	require.NoError(t, json.Unmarshal([]byte(data), &responses))
	return responses
}
