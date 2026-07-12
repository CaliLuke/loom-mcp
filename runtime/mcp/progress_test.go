package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReportProgressUsesContextReporterAndToken(t *testing.T) {
	update := ProgressUpdate{Progress: 1, Total: 3, Message: "started"}
	reporter := progressReporterFunc(func(_ context.Context, token any, got ProgressUpdate) error {
		require.Equal(t, "token-1", token)
		require.Equal(t, update, got)
		return nil
	})
	ctx := WithProgressToken(WithProgressReporter(context.Background(), reporter), "token-1")

	err := ReportProgress(ctx, update)

	require.NoError(t, err)
}

func TestReportProgressReturnsUnavailableWithoutReporter(t *testing.T) {
	err := ReportProgress(WithProgressToken(context.Background(), "token-1"), ProgressUpdate{})

	require.ErrorIs(t, err, ErrProgressReporterUnavailable)
}

func TestReportProgressReturnsUnavailableWithoutToken(t *testing.T) {
	reporter := progressReporterFunc(func(context.Context, any, ProgressUpdate) error { return nil })

	err := ReportProgress(WithProgressReporter(context.Background(), reporter), ProgressUpdate{})

	require.ErrorIs(t, err, ErrProgressTokenUnavailable)
}

func TestReportProgressReturnsReporterErrors(t *testing.T) {
	wantErr := errors.New("client rejected progress")
	reporter := progressReporterFunc(func(context.Context, any, ProgressUpdate) error { return wantErr })
	ctx := WithProgressToken(WithProgressReporter(context.Background(), reporter), 42)

	err := ReportProgress(ctx, ProgressUpdate{})

	require.ErrorIs(t, err, wantErr)
}

type progressReporterFunc func(context.Context, any, ProgressUpdate) error

func (f progressReporterFunc) ReportProgress(ctx context.Context, token any, update ProgressUpdate) error {
	return f(ctx, token, update)
}
