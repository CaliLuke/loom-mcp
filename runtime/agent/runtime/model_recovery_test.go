package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type concurrentRecoveryClient struct {
	rejected *model.OutputValidationError
	releaseB <-chan struct{}
}

func (c *concurrentRecoveryClient) Complete(_ context.Context, request *model.Request) (*model.Response, error) {
	if request.Model == "model-b" {
		<-c.releaseB
		return &model.Response{}, nil
	}
	return nil, c.rejected
}

func (c *concurrentRecoveryClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, errors.New("stream is not supported")
}

func TestModelRecoveryRecorderBuildsBoundedToolIdentityCorrection(t *testing.T) {
	t.Parallel()

	validationErr := restoredValidationError(t, model.OutputValidationToolIdentity)
	recorder := &modelRecoveryRecorder{}
	recorder.record(&model.Request{Tools: []*model.ToolDefinition{
		{Name: "zeta.lookup"},
		{Name: "alpha.read"},
	}}, validationErr)

	recovery, err := recorder.recovery(validationErr, 4)
	require.NoError(t, err)
	require.Equal(t, model.OutputValidationToolIdentity, recovery.Kind)
	require.Equal(t, 37, recovery.ByteCount)
	require.Equal(t, byte(9), recovery.Fingerprint[0])
	require.Equal(t, model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}, recovery.Usage)
	require.Equal(t, 4, recovery.Attempt)
	require.Equal(t, "Replace the rejected tool call. Use one of these exact advertised tool names: alpha.read, zeta.lookup", recovery.Correction)
	require.False(t, recovery.DisableTools)
	require.LessOrEqual(t, len(recovery.Correction), maxRecoveryCorrectionBytes)
}

func TestModelRecoveryRecorderDescribesOnlyToolFieldContracts(t *testing.T) {
	t.Parallel()

	validationErr := restoredValidationError(t, model.OutputValidationToolArguments)
	recorder := &modelRecoveryRecorder{}
	recorder.record(&model.Request{Tools: []*model.ToolDefinition{{
		Name: "orders.create",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"account_id"},
			"properties": map[string]any{
				"account_id": map[string]any{"type": "string", "example": "private-account"},
				"quantity":   map[string]any{"type": "integer"},
			},
			"example": map[string]any{"account_id": "private-account", "quantity": 99},
		},
	}}}, validationErr)

	recovery, err := recorder.recovery(validationErr, 1)
	require.NoError(t, err)
	require.Contains(t, recovery.Correction, "account_id:string,required")
	require.Contains(t, recovery.Correction, "quantity:integer")
	require.NotContains(t, recovery.Correction, "private-account")
	require.NotContains(t, recovery.Correction, "99")
	require.LessOrEqual(t, len(recovery.Correction), maxRecoveryCorrectionBytes)
}

func TestModelRecoveryRecorderDisablesToolsForFinalAnswerReplacement(t *testing.T) {
	t.Parallel()

	for _, kind := range []model.OutputValidationKind{
		model.OutputValidationOutputBounds,
		model.OutputValidationStructuredOutput,
	} {
		t.Run(string(kind), func(t *testing.T) {
			validationErr := restoredValidationError(t, kind)
			recorder := &modelRecoveryRecorder{}
			recorder.record(&model.Request{}, validationErr)

			recovery, err := recorder.recovery(validationErr, 2)
			require.NoError(t, err)
			require.True(t, recovery.DisableTools)
			require.Nil(t, recovery.ToolCatalog)
			require.NotEmpty(t, strings.TrimSpace(recovery.Correction))
		})
	}
}

func TestModelRecoveryRecorderKeepsMixedAndUnsupportedFailuresTerminal(t *testing.T) {
	t.Parallel()

	t.Run("mixed failure", func(t *testing.T) {
		validationErr := restoredValidationError(t, model.OutputValidationToolIdentity)
		terminalErr := errors.Join(validationErr, errors.New("transport failed"))
		recorder := &modelRecoveryRecorder{}
		recorder.record(&model.Request{}, terminalErr)

		recovery, err := recorder.recovery(terminalErr, 1)
		require.Nil(t, recovery)
		require.ErrorIs(t, err, validationErr)
		require.ErrorContains(t, err, "transport failed")
	})

	t.Run("unsupported validation", func(t *testing.T) {
		validationErr := restoredValidationError(t, model.OutputValidationUsage)
		recorder := &modelRecoveryRecorder{}
		recorder.record(&model.Request{}, validationErr)

		recovery, err := recorder.recovery(validationErr, 1)
		require.Nil(t, recovery)
		require.ErrorIs(t, err, validationErr)
	})
}

func TestModelRecoveryRecorderRequiresFinalPlannerFailure(t *testing.T) {
	t.Parallel()

	validationErr := restoredValidationError(t, model.OutputValidationToolIdentity)
	recorder := &modelRecoveryRecorder{}
	recorder.record(&model.Request{Tools: []*model.ToolDefinition{{Name: "svc.read"}}}, validationErr)

	recovery, err := recorder.recovery(nil, 1)
	require.NoError(t, err)
	require.Nil(t, recovery)
}

func TestModelRecoveryRecorderSelectsPlannerReturnedConcurrentFailure(t *testing.T) {
	t.Parallel()

	rejected := restoredValidationError(t, model.OutputValidationToolArguments)
	recorder := &modelRecoveryRecorder{}
	releaseB := make(chan struct{})
	modelBErr := make(chan error, 1)
	client := newRecoveryCapturingClient(&concurrentRecoveryClient{rejected: rejected, releaseB: releaseB}, recorder)
	go func() {
		_, err := client.Complete(context.Background(), &model.Request{
			Model: "model-b",
			Tools: []*model.ToolDefinition{{Name: "svc.success"}},
		})
		modelBErr <- err
	}()

	_, err := client.Complete(context.Background(), &model.Request{
		Model: "model-a",
		Tools: []*model.ToolDefinition{{Name: "svc.rejected"}},
	})
	require.ErrorIs(t, err, rejected)
	close(releaseB)
	require.NoError(t, <-modelBErr)

	recovery, err := recorder.recovery(rejected, 2)
	require.NoError(t, err)
	require.NotNil(t, recovery)
	require.Equal(t, model.OutputValidationToolArguments, recovery.Kind)
	require.Equal(t, []tools.Ident{"svc.rejected"}, recovery.ToolCatalog)
}

func TestModelRecoveryRecorderFailsClosedAtRecordLimit(t *testing.T) {
	t.Parallel()

	recorder := &modelRecoveryRecorder{}
	var rejected *model.OutputValidationError
	for range maxModelRecoveryRecords + 1 {
		rejected = restoredValidationError(t, model.OutputValidationToolArguments)
		recorder.record(&model.Request{}, rejected)
	}

	recovery, err := recorder.recovery(rejected, 1)
	require.Nil(t, recovery)
	require.ErrorIs(t, err, rejected)
	require.ErrorContains(t, err, "model recovery record limit exceeded")
	_, err = recorder.activityUsage(model.TokenUsage{})
	require.ErrorContains(t, err, "model recovery record limit exceeded")
}

func restoredValidationError(t *testing.T, kind model.OutputValidationKind) *model.OutputValidationError {
	t.Helper()

	fingerprint := [32]byte{9, 8, 7}
	validationErr, err := model.RestoreOutputValidationError(
		kind,
		errors.New("private rejected output: secret-value"),
		model.ResponseEvidence{Present: true, ByteCount: 37, Fingerprint: fingerprint},
		&model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
	)
	require.NoError(t, err)
	return validationErr
}
