package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/CaliLuke/loom/pulse/streaming"
	streamopts "github.com/CaliLuke/loom/pulse/streaming/options"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failureRegistryClient struct {
	ref               toolregistry.ToolCallRef
	retryRef          toolregistry.ToolCallRef
	callErr, retryErr error
	calls, retries    int
}

type failurePulseClient struct {
	pulse.Client
	stream pulse.Stream
	err    error
}

type failureReaderStream struct {
	pulse.Stream
	err error
}

func TestExecutorRejectsInvalidSetupBeforeAdmission(t *testing.T) {
	call := &planner.ToolRequest{Name: "docs.read"}
	meta := &agentsruntime.ToolCallMeta{ToolCallID: "call"}
	for _, tc := range []struct {
		name   string
		call   *planner.ToolRequest
		meta   *agentsruntime.ToolCallMeta
		client Client
		pulse  pulse.Client
		specs  SpecLookup
		want   string
	}{
		{name: "nil call", want: "tool request is nil"},
		{name: "nil metadata", call: call, want: "tool call meta is nil"},
		{name: "nil registry", call: call, meta: meta, want: "registry client is nil"},
		{name: "nil pulse", call: call, meta: meta, client: &failureRegistryClient{}, want: "pulse client is nil"},
		{name: "nil specs", call: call, meta: meta, client: &failureRegistryClient{}, pulse: failurePulseClient{}, want: "tool specs lookup is nil"},
		{name: "unknown tool", call: call, meta: meta, client: &failureRegistryClient{}, pulse: failurePulseClient{}, specs: fakeSpecs{}, want: "unknown tool"},
		{name: "missing routing", call: call, meta: meta, client: &failureRegistryClient{}, pulse: failurePulseClient{}, specs: fakeSpecs{spec: &tools.ToolSpec{Name: "docs.read"}}, want: "missing toolset routing id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := New(tc.client, tc.pulse, tc.specs, nil, WithLogger(telemetry.NewNoopLogger()))
			result, err := executor.Execute(t.Context(), tc.meta, tc.call)
			require.NoError(t, err)
			require.NotNil(t, result.ToolResult.Error)
			assert.Contains(t, result.ToolResult.Error.Error(), tc.want)
			if c, ok := tc.client.(*failureRegistryClient); ok {
				assert.Zero(t, c.calls)
			}
		})
	}
}

func TestExecutorSeparatesRejectedFromAmbiguousAdmission(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		unknown bool
		retry   planner.RetryReason
	}{
		{"missing", loom.PermanentError("not_found", "missing"), false, planner.RetryReasonToolUnavailable},
		{"rejected", loom.PermanentError("call_not_admitted", "unhealthy"), false, planner.RetryReasonToolUnavailable},
		{"invalid input", loom.PermanentError("validation_error", "invalid"), false, ""},
		{"transport validation", loom.PermanentError(loom.MissingField, "missing"), false, ""},
		{"other service failure", loom.PermanentError("service_unavailable", "lost"), true, ""},
		{"connection lost", errors.New("connection lost"), true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &failureRegistryClient{callErr: fmt.Errorf("wrapped: %w", tc.err)}
			executor := New(client, failurePulseClient{}, fakeSpecs{spec: &tools.ToolSpec{Name: "docs.read", Toolset: "docs"}})
			result, err := executor.Execute(t.Context(), &agentsruntime.ToolCallMeta{ToolCallID: "call"}, &planner.ToolRequest{Name: "docs.read"})
			require.NoError(t, err)
			require.NotNil(t, result.ToolResult.Error)
			assert.Equal(t, tc.unknown, result.ToolResult.Error.Kind == planner.ToolErrorKindOutcomeUnknown)
			if tc.retry != "" {
				require.NotNil(t, result.ToolResult.RetryHint)
				assert.Equal(t, tc.retry, result.ToolResult.RetryHint.Reason)
			} else {
				assert.Nil(t, result.ToolResult.RetryHint)
			}
			assert.Equal(t, 1, client.calls)
			assert.Zero(t, client.retries)
		})
	}
}

func TestExecutorNeverReexecutesAfterAmbiguousResultFailure(t *testing.T) {
	ref := toolregistry.ToolCallRef{ToolUseID: "use", RegistrationToken: testRegistrationTokenA, ExecutionDeadline: time.Now().Truncate(time.Millisecond).Add(time.Minute), ResultStreamExpiresAt: time.Now().Truncate(time.Millisecond).Add(time.Hour)}
	require.NoError(t, toolregistry.ValidateToolCallRef(ref))
	retryEvent := &streaming.Event{ID: "1-0", EventName: toolregistry.ResultEventKey, Payload: mustJSON(t, toolregistry.ToolResultMessage{ToolUseID: "use", RegistrationToken: testRegistrationTokenA, Retry: &toolregistry.ToolRetry{Reason: toolregistry.ToolRetryReasonProviderOverloaded, RetryAfterMillis: 1}})}
	for _, tc := range []struct {
		name                           string
		streamErr, readerErr, retryErr error
		closed                         bool
		retryRef                       toolregistry.ToolCallRef
	}{
		{name: "stream unavailable", streamErr: assert.AnError},
		{name: "reader DNS", readerErr: &net.DNSError{Name: "redis", Server: "127.0.0.1", IsTimeout: true, IsTemporary: true}},
		{name: "reader generic", readerErr: assert.AnError},
		{name: "closed subscription", closed: true},
		{name: "retry failed", retryErr: assert.AnError},
		{name: "retry invalid reference"},
		{name: "retry changed call", retryRef: toolregistry.ToolCallRef{ToolUseID: "replacement", RegistrationToken: ref.RegistrationToken, ExecutionDeadline: ref.ExecutionDeadline, ResultStreamExpiresAt: ref.ResultStreamExpiresAt}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &fakeStream{t: t, requiredStart: "0"}
			if !tc.closed {
				stream.events = []*streaming.Event{retryEvent}
			}
			var inputStream pulse.Stream = stream
			if tc.readerErr != nil {
				inputStream = failureReaderStream{err: tc.readerErr}
			}
			client := &failureRegistryClient{ref: ref, retryRef: tc.retryRef, retryErr: tc.retryErr}
			executor := New(client, failurePulseClient{stream: inputStream, err: tc.streamErr}, fakeSpecs{spec: &tools.ToolSpec{Name: "docs.read", Toolset: "docs"}})
			result, err := executor.Execute(t.Context(), &agentsruntime.ToolCallMeta{RunID: "run", SessionID: "session", ToolCallID: "call"}, &planner.ToolRequest{Name: "docs.read"})
			require.NoError(t, err)
			require.NotNil(t, result.ToolResult.Error)
			assert.Equal(t, planner.ToolErrorKindOutcomeUnknown, result.ToolResult.Error.Kind)
			assert.Nil(t, result.ToolResult.RetryHint, "uncertain execution must not invite a replacement call")
			assert.Equal(t, 1, client.calls)
			if tc.closed || tc.readerErr != nil || tc.streamErr != nil {
				assert.Zero(t, client.retries)
			} else {
				assert.Equal(t, 1, client.retries)
			}
		})
	}
}

func (c *failureRegistryClient) CallTool(context.Context, string, tools.Ident, []byte, toolregistry.ToolCallMeta) (toolregistry.ToolCallRef, error) {
	c.calls++
	return c.ref, c.callErr
}

func (c *failureRegistryClient) RetryTool(context.Context, string, tools.Ident, []byte, toolregistry.ToolCallMeta, string) (toolregistry.ToolCallRef, error) {
	c.retries++
	return c.retryRef, c.retryErr
}

func (c failurePulseClient) Stream(string, ...streamopts.Stream) (pulse.Stream, error) {
	return c.stream, c.err
}

func (s failureReaderStream) NewReader(context.Context, ...streamopts.Reader) (pulse.Reader, error) {
	return nil, s.err
}
