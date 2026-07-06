package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestElicitUsesContextElicitor(t *testing.T) {
	request := ElicitRequest{
		Mode:    "form",
		Message: "Provide a value",
		RequestedSchema: map[string]any{
			"type": "object",
		},
	}
	want := &ElicitResult{
		Action: "accept",
		Content: map[string]any{
			"value": "ok",
		},
	}
	elicitor := elicitorFunc(func(ctx context.Context, got ElicitRequest) (*ElicitResult, error) {
		require.Equal(t, request, got)
		return want, nil
	})

	result, err := Elicit(WithElicitor(context.Background(), elicitor), request)

	require.NoError(t, err)
	require.Equal(t, want, result)
}

func TestElicitReturnsUnavailableWithoutContextElicitor(t *testing.T) {
	_, err := Elicit(context.Background(), ElicitRequest{Message: "missing"})

	require.ErrorIs(t, err, ErrElicitorUnavailable)
}

func TestElicitReturnsElicitorErrors(t *testing.T) {
	wantErr := errors.New("client rejected elicitation")
	elicitor := elicitorFunc(func(context.Context, ElicitRequest) (*ElicitResult, error) {
		return nil, wantErr
	})

	_, err := Elicit(WithElicitor(context.Background(), elicitor), ElicitRequest{Message: "fail"})

	require.ErrorIs(t, err, wantErr)
}

type elicitorFunc func(context.Context, ElicitRequest) (*ElicitResult, error)

func (f elicitorFunc) Elicit(ctx context.Context, req ElicitRequest) (*ElicitResult, error) {
	return f(ctx, req)
}
