package sdkclient

import (
	"context"
	"testing"

	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	"github.com/stretchr/testify/require"
)

func TestWithClientFeaturesIgnoresNilSession(t *testing.T) {
	ctx := WithClientFeatures(context.Background(), nil)

	_, hasElicitor := mcpruntime.ElicitorFromContext(ctx)
	_, hasSampler := mcpruntime.SamplerFromContext(ctx)
	_, hasRootLister := mcpruntime.RootListerFromContext(ctx)
	_, hasProgressReporter := mcpruntime.ProgressReporterFromContext(ctx)
	require.False(t, hasElicitor)
	require.False(t, hasSampler)
	require.False(t, hasRootLister)
	require.False(t, hasProgressReporter)
}
