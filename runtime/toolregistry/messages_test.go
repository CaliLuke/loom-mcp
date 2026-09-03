package toolregistry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestValidateToolResultMessageRejectsAggregateOversize(t *testing.T) {
	t.Parallel()

	message := NewToolResultMessage(
		strings.Repeat("a", 64),
		"use-1",
		json.RawMessage(`{"value":"`+strings.Repeat("x", MaxToolResultMessageBytes)+`"}`),
	)
	require.ErrorContains(t, ValidateToolResultMessage(message), "exceeds")
}

func TestMessageConstructorsPreserveCanonicalWireFields(t *testing.T) {
	const registrationToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	meta := &ToolCallMeta{RunID: "run-1", SessionID: "session-1", ToolCallID: "call-1"}
	payload := json.RawMessage(`{"query":"status"}`)
	executionDeadline := time.UnixMilli(1000)
	resultExpiration := time.UnixMilli(2000)
	call := NewToolCallMessage(
		registrationToken,
		"use-1",
		executionDeadline,
		resultExpiration,
		tools.Ident("service.search"),
		payload,
		meta,
	)
	assert.Equal(t, MessageTypeCall, call.Type)
	assert.Equal(t, registrationToken, call.RegistrationToken)
	assert.Equal(t, "use-1", call.ToolUseID)
	assert.Equal(t, executionDeadline.UnixMilli(), call.ExecutionDeadlineUnixMilli)
	assert.Equal(t, resultExpiration.UnixMilli(), call.ResultStreamExpiresAtUnixMilli)
	assert.Equal(t, tools.Ident("service.search"), call.Tool)
	assert.JSONEq(t, string(payload), string(call.Payload))
	assert.Same(t, meta, call.Meta)

	ping := NewPingMessage(registrationToken, "ping-1")
	assert.Equal(t, ToolCallMessage{RegistrationToken: registrationToken, Type: MessageTypePing, PingID: "ping-1"}, ping)

	result := NewToolResultMessage(registrationToken, "use-1", json.RawMessage(`{"ok":true}`))
	assert.Equal(t, registrationToken, result.RegistrationToken)
	assert.Equal(t, "use-1", result.ToolUseID)
	assert.JSONEq(t, `{"ok":true}`, string(result.Result))

	serverData := []*ServerDataItem{{Kind: "card", Audience: "ui", Data: json.RawMessage(`{"title":"Done"}`)}}
	withServerData := NewToolResultMessageWithServerData(registrationToken, "use-1", result.Result, serverData)
	assert.Equal(t, serverData, withServerData.ServerData)

	delta := NewToolOutputDeltaMessage(registrationToken, "use-1", "stdout", "ready\n")
	assert.Equal(t, ToolOutputDeltaMessage{RegistrationToken: registrationToken, ToolUseID: "use-1", Stream: "stdout", Delta: "ready\n"}, delta)

	failure := NewToolResultErrorMessage(registrationToken, "use-1", "invalid_arguments", "query is required")
	require.NotNil(t, failure.Error)
	assert.Equal(t, "invalid_arguments", failure.Error.Code)
	assert.Equal(t, "query is required", failure.Error.Message)
}
func TestServiceErrorMessageUsesOneSafeRemedySnapshot(t *testing.T) {
	err := &changingRemedyError{}

	message := NewToolResultServiceErrorMessage(
		"registration-token",
		"use-1",
		tools.Ident("service.lookup"),
		"execution_failed",
		err,
	)

	require.NotNil(t, message.Error)
	assert.Equal(t, "Lookup is unavailable.", message.Error.Message)
	assert.Equal(t, 1, err.calls)
}

func TestValidationIssueMessagesCloneCallerOwnedData(t *testing.T) {
	const registrationToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	allowed := []string{"fast", "safe"}
	issues := []*tools.FieldIssue{{Field: "mode", Constraint: loom.InvalidEnumValue, Allowed: allowed}, nil}
	message := NewToolResultInvalidArgumentsMessage(registrationToken, "use-1", "bad mode", issues)

	allowed[0] = "mutated"
	issues[0].Field = "changed"
	require.NotNil(t, message.Error)
	require.Len(t, message.Error.Issues, 1)
	assert.Equal(t, "mode", message.Error.Issues[0].Field)
	assert.Equal(t, []string{"fast", "safe"}, message.Error.Issues[0].Allowed)

	assert.Nil(t, NewToolResultInvalidArgumentsMessage(registrationToken, "use-1", "boom", nil).Error.Issues)
}

func TestValidationIssuesSupportsGeneratedAndGoaErrors(t *testing.T) {
	generated := &validationIssueError{issues: []*tools.FieldIssue{
		{Field: "query", Constraint: loom.MissingField, Allowed: []string{"value"}},
		nil,
	}}
	got := ValidationIssues(generated)
	require.Len(t, got, 1)
	assert.Equal(t, "query", got[0].Field)
	assert.Equal(t, []string{"value"}, got[0].Allowed)
	generated.issues[0].Allowed[0] = "mutated"
	assert.Equal(t, []string{"value"}, got[0].Allowed)

	wrapped := ValidationIssues(errors.Join(errors.New("decode"), loom.MissingFieldError("body.topic", "request body")))
	require.Len(t, wrapped, 1)
	assert.Equal(t, "topic", wrapped[0].Field)
	assert.Equal(t, loom.MissingField, wrapped[0].Constraint)

	assert.Nil(t, ValidationIssues(nil))
	assert.Nil(t, ValidationIssues(errors.New("ordinary failure")))
	assert.Nil(t, ValidationIssues(loom.PermanentError("unavailable", "offline")))
}

func TestTraceContextRoundTrip(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	traceState, err := trace.ParseTraceState("vendor=value")
	require.NoError(t, err)
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		TraceState: traceState,
	})
	member, err := baggage.NewMember("tenant", "acme")
	require.NoError(t, err)
	bag, err := baggage.New(member)
	require.NoError(t, err)
	ctx := baggage.ContextWithBaggage(trace.ContextWithSpanContext(context.Background(), spanContext), bag)

	traceParent, encodedState, encodedBaggage := InjectTraceContext(ctx)
	assert.Equal(t, "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01", traceParent)
	assert.Equal(t, "vendor=value", encodedState)
	assert.Equal(t, "tenant=acme", encodedBaggage)

	extracted := ExtractTraceContext(context.Background(), traceParent, encodedState, encodedBaggage)
	assert.Equal(t, spanContext.TraceID(), trace.SpanContextFromContext(extracted).TraceID())
	assert.Equal(t, spanContext.SpanID(), trace.SpanContextFromContext(extracted).SpanID())
	assert.True(t, trace.SpanContextFromContext(extracted).IsRemote())
	assert.Equal(t, "acme", baggage.FromContext(extracted).Member("tenant").Value())

	background := context.Background()
	assert.Equal(t, background, ExtractTraceContext(background, "", "", ""))
	emptyParent, emptyState, emptyBaggage := InjectTraceContext(background)
	assert.Empty(t, emptyParent)
	assert.Empty(t, emptyState)
	assert.Empty(t, emptyBaggage)
}

func TestStreamNamesAndOutputDeltaPublisherContext(t *testing.T) {
	assert.Equal(t, "toolset:catalog:requests", ToolsetStreamID("catalog"))
	assert.Equal(t, "result:use-1", ResultStreamID("use-1"))

	_, ok := OutputDeltaPublisherFromContext(context.Background())
	assert.False(t, ok)
	pub := &recordingDeltaPublisher{}
	ctx := WithOutputDeltaPublisher(context.Background(), pub)
	got, ok := OutputDeltaPublisherFromContext(ctx)
	require.True(t, ok)
	assert.Same(t, pub, got)
	require.NoError(t, got.PublishToolOutputDelta(ctx, "stdout", "ready"))
	assert.Equal(t, "stdout", pub.stream)
	assert.Equal(t, "ready", pub.delta)
}

type changingRemedyError struct {
	calls int
}

func (e *changingRemedyError) Error() string {
	return "query database: password=secret"
}

func (e *changingRemedyError) LoomErrorRemedy() *loom.ErrorRemedy {
	e.calls++
	if e.calls == 1 {
		return &loom.ErrorRemedy{SafeMessage: "Lookup is unavailable."}
	}
	return nil
}

type validationIssueError struct {
	issues []*tools.FieldIssue
}

func (e *validationIssueError) Error() string {
	return "validation failed"
}

func (e *validationIssueError) Issues() []*tools.FieldIssue {
	return e.issues
}

type recordingDeltaPublisher struct {
	stream string
	delta  string
}

func (p *recordingDeltaPublisher) PublishToolOutputDelta(_ context.Context, stream string, delta string) error {
	p.stream = stream
	p.delta = delta
	return nil
}
