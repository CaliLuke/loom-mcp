package temporal

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/protobuf/proto"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
)

// legacyProviderEnvelope fixes the pre-E1 payload shape independently of hooks.
type legacyProviderEnvelope struct {
	Provider   string
	Operation  string
	HTTPStatus int
	Kind       string
	Code       string
	Message    string
	RequestID  string
	Retryable  bool
}

func TestApplicationErrorConversions(t *testing.T) {
	for _, test := range []struct {
		name         string
		nonRetryable bool
		details      any
	}{
		{name: "retryable", details: map[string]any{"code": "bad"}},
		{name: "nonretryable", nonRetryable: true, details: map[string]any{"code": "bad"}},
		{name: "no details"},
	} {
		t.Run(test.name, func(t *testing.T) {
			neutral := &engine.ApplicationError{Type: "example.error", Message: "failed", NonRetryable: test.nonRetryable, Details: test.details, Cause: errors.New("cause")}
			sdkErr := classifyTemporalCancellation(neutral)
			var sdkApplication *temporal.ApplicationError
			require.ErrorAs(t, sdkErr, &sdkApplication)
			assert.Equal(t, neutral.Type, sdkApplication.Type())
			assert.Equal(t, neutral.Message, sdkApplication.Message())
			assert.Equal(t, neutral.NonRetryable, sdkApplication.NonRetryable())
			require.ErrorIs(t, sdkApplication, neutral.Cause)
			previous := temporal.NewApplicationErrorWithOptions(neutral.Message, neutral.Type, temporal.ApplicationErrorOptions{NonRetryable: neutral.NonRetryable, Cause: neutral.Cause})
			if test.details != nil {
				previous = temporal.NewApplicationErrorWithOptions(neutral.Message, neutral.Type, temporal.ApplicationErrorOptions{NonRetryable: neutral.NonRetryable, Cause: neutral.Cause, Details: []any{test.details}})
			}
			converter := temporal.GetDefaultFailureConverter()
			failure := converter.ErrorToFailure(sdkErr)
			assert.True(t, proto.Equal(converter.ErrorToFailure(previous), failure), "serialized failure must match previous SDK construction")
			wireErr := converter.FailureToError(failure)
			received := normalizeTemporalError(wireErr)
			var application *engine.ApplicationError
			require.ErrorAs(t, received, &application)
			assert.Equal(t, neutral.Type, application.Type)
			assert.Equal(t, neutral.Message, application.Message)
			assert.Equal(t, neutral.NonRetryable, application.NonRetryable)
			assert.Equal(t, test.details, application.Details)
			require.ErrorIs(t, application, wireErr)
			require.ErrorAs(t, received, &sdkApplication)
			assert.True(t, proto.Equal(failure, converter.ErrorToFailure(classifyTemporalCancellation(received))), "returning a workflow error must preserve its original wire failure")
		})
	}
}

func TestTemporalErrorMarkers(t *testing.T) {
	for _, test := range []struct {
		name              string
		err               error
		canceled, timeout bool
	}{
		{name: "nil"},
		{name: "ordinary", err: errors.New("failure")},
		{name: "canceled", err: temporal.NewCanceledError("detail"), canceled: true},
		{name: "wrapped canceled", err: fmt.Errorf("wrapped: %w", temporal.NewCanceledError()), canceled: true},
		{name: "timeout", err: temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_START_TO_CLOSE, errors.New("cause")), timeout: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeTemporalError(test.err)
			assert.Equal(t, test.canceled, engine.IsCanceled(got))
			assert.Equal(t, test.timeout, engine.IsTimeout(got))
			switch {
			case test.canceled:
				assert.True(t, cancellation.Only(got))
				require.ErrorIs(t, got, context.Canceled)
				var sdkCanceled *temporal.CanceledError
				require.ErrorAs(t, got, &sdkCanceled)
			case test.timeout:
				var timeout *engine.TimeoutError
				require.ErrorAs(t, got, &timeout)
				assert.Same(t, test.err, timeout.Cause)
				var sdkTimeout *temporal.TimeoutError
				require.ErrorAs(t, got, &sdkTimeout)
			default:
				assert.Equal(t, test.err, got)
			}
		})
	}
}

func TestProviderErrorWireEnvelope(t *testing.T) {
	for _, retryable := range []bool{false, true} {
		t.Run(fmt.Sprint(retryable), func(t *testing.T) {
			provider := model.NewProviderError("bedrock", "converse_stream", 429, model.ProviderErrorKindRateLimited, "ThrottlingException", "too many requests", "req-1", retryable, errors.New("throttled"))
			wrapped := hooks.WrapRunCompletionError(provider)
			var application *engine.ApplicationError
			require.ErrorAs(t, wrapped, &application)
			legacy := legacyProviderEnvelope{Provider: "bedrock", Operation: "converse_stream", HTTPStatus: 429, Kind: "rate_limited", Code: "ThrottlingException", Message: "too many requests", RequestID: "req-1", Retryable: retryable}
			previous := temporal.NewApplicationErrorWithCause(provider.Error(), "loom_mcp.provider_error", provider, legacy)
			if !retryable {
				previous = temporal.NewNonRetryableApplicationError(provider.Error(), "loom_mcp.provider_error", provider, legacy)
			}
			converter := temporal.GetDefaultFailureConverter()
			failure := converter.ErrorToFailure(classifyTemporalCancellation(wrapped))
			assert.True(t, proto.Equal(converter.ErrorToFailure(previous), failure))
			received := normalizeTemporalError(converter.FailureToError(failure))
			event := hooks.NewRunCompletedEvent("run", "agent", "session", "failed", run.PhaseFailed, received)
			assert.Equal(t, "bedrock", event.ErrorProvider)
			assert.Equal(t, "ThrottlingException", event.ErrorCode)
			assert.Equal(t, retryable, event.Retryable)
			assert.Equal(t, hooks.PublicErrorProviderRateLimited, event.PublicError)
		})
	}
}

func TestNestedTemporalProviderEnvelope(t *testing.T) {
	provider := model.NewProviderError("bedrock", "converse_stream", 429, model.ProviderErrorKindRateLimited, "ThrottlingException", "too many requests", "req-1", true, nil)
	inner := classifyTemporalCancellation(hooks.WrapRunCompletionError(provider))
	outer := temporal.NewNonRetryableApplicationError("outer failed", "example.outer", inner)
	converter := temporal.GetDefaultFailureConverter()
	failure := converter.ErrorToFailure(outer)
	original := converter.FailureToError(failure)
	got := normalizeTemporalError(original)
	var application *engine.ApplicationError
	require.ErrorAs(t, got, &application)
	assert.Equal(t, "example.outer", application.Type)
	event := hooks.NewRunCompletedEvent("run", "agent", "session", "failed", run.PhaseFailed, got)
	assert.Equal(t, "bedrock", event.ErrorProvider)
	assert.True(t, proto.Equal(failure, converter.ErrorToFailure(classifyTemporalCancellation(got))))
}

func TestMixedProviderErrorWireEnvelope(t *testing.T) {
	provider := model.NewProviderError("bedrock", "converse_stream", 429, model.ProviderErrorKindRateLimited, "ThrottlingException", "too many requests", "req-1", true, nil)
	mixed := errors.Join(context.Canceled, provider)
	wrapped := hooks.WrapRunCompletionError(mixed)
	var application *engine.ApplicationError
	require.ErrorAs(t, wrapped, &application)
	previous := temporal.NewApplicationErrorWithCause(provider.Error(), "loom_mcp.provider_error", mixed, legacyProviderEnvelope{Provider: "bedrock", Operation: "converse_stream", HTTPStatus: 429, Kind: "rate_limited", Code: "ThrottlingException", Message: "too many requests", RequestID: "req-1", Retryable: true})
	expected := temporal.NewNonRetryableApplicationError("operation failed with mixed cancellation and failure", mixedCancellationFailureType, previous)
	converter := temporal.GetDefaultFailureConverter()
	assert.True(t, proto.Equal(converter.ErrorToFailure(expected), converter.ErrorToFailure(classifyTemporalCancellation(wrapped))))
}

func TestClassifyTemporalErrorPreservesOuterGraph(t *testing.T) {
	converter := temporal.GetDefaultFailureConverter()
	sdk := temporal.NewNonRetryableApplicationError("boom", "example.error", nil)
	received := normalizeTemporalError(converter.FailureToError(converter.ErrorToFailure(sdk)))
	seal := errors.New("seal failed")
	for _, test := range []struct {
		name string
		err  error
		leaf error
	}{
		{name: "seal join", err: errors.Join(received, seal), leaf: seal},
		{name: "wrapped reason", err: fmt.Errorf("finalization failed: %w", received)},
		{name: "joined nonretryable application", err: errors.Join(engine.NewNonRetryableApplicationError("boom", "example.error", nil, nil), seal), leaf: seal},
		{name: "wrapped neutral application", err: fmt.Errorf("wrapped: %w", engine.NewNonRetryableApplicationError("boom", "example.error", nil, nil))},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := classifyTemporalCancellation(test.err)
			assert.Same(t, test.err, got, "only a root engine error or synthetic adapter chain may be converted")
			assert.Equal(t, test.err.Error(), got.Error())
			if test.leaf != nil {
				require.ErrorIs(t, got, test.leaf)
			}
			before := converter.ErrorToFailure(test.err)
			after := converter.ErrorToFailure(got)
			assert.True(t, proto.Equal(before, after))
			require.NotNil(t, after.GetApplicationFailureInfo())
			assert.False(t, after.GetApplicationFailureInfo().NonRetryable, "outer joins and reason wrappers keep their pre-E1 retryability")
		})
	}
}
