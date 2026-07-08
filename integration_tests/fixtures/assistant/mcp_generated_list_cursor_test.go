package assistantapi

import (
	"context"
	"testing"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedAdapterListMethodsRejectNonEmptyCursor(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, nil)
	cursor := "next-page"

	tests := []struct {
		name    string
		invoke  func(context.Context) error
		message string
	}{
		{
			name: "tools",
			invoke: func(ctx context.Context) error {
				_, err := adapter.ToolsList(ctx, &mcpassistant.ToolsListPayload{Cursor: &cursor})
				return err
			},
			message: "tools/list pagination is not implemented; cursor must be empty",
		},
		{
			name: "resources",
			invoke: func(ctx context.Context) error {
				_, err := adapter.ResourcesList(ctx, &mcpassistant.ResourcesListPayload{Cursor: &cursor})
				return err
			},
			message: "resources/list pagination is not implemented; cursor must be empty",
		},
		{
			name: "prompts",
			invoke: func(ctx context.Context) error {
				_, err := adapter.PromptsList(ctx, &mcpassistant.PromptsListPayload{Cursor: &cursor})
				return err
			},
			message: "prompts/list pagination is not implemented; cursor must be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.invoke(context.Background())
			require.Error(t, err)

			var named loom.LoomErrorNamer
			require.ErrorAs(t, err, &named)
			assert.Equal(t, "invalid_params", named.LoomErrorName())
			assert.Equal(t, tt.message, loom.ErrorSafeMessage(err))
		})
	}
}
