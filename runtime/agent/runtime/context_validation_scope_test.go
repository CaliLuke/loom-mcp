package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextBudgetValidationScope(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		window, keep, diagnostics int
	}{{"defaults below floor", 2000, 0, 1}, {"registered residents", 6000, 1000, 0}} {
		for _, explicit := range []bool{false, true} {
			name := tc.name + "/registered"
			if explicit {
				name = tc.name + "/explicit"
			}
			t.Run(name, func(t *testing.T) {
				rt := New(WithContextCompaction("content.agent", ContextCompaction{KeepRecentTokens: tc.keep}))
				provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: tc.window}, resolved: "model"}
				require.NoError(t, rt.RegisterModel("fake", provider))
				registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
					client, ok := in.Agent.ModelClient("fake")
					require.True(t, ok)
					if _, err := client.Complete(ctx, &model.Request{Messages: in.Messages}); err != nil {
						return nil, err
					}
					return oracleFinal(), nil
				}})
				opts := []RunOption{WithRunID("root")}
				if explicit {
					opts = append(opts, WithContextOverrides(ContextOverrides{}))
				}
				_, err := rt.MustClient("content.agent").Run(t.Context(), "session", []*model.Message{contentMessage("hello")}, opts...)
				if explicit {
					var invalid *ContextSettingsError
					require.ErrorAs(t, err, &invalid)
					assert.Equal(t, "ResidentTokens", invalid.Field)
					assert.Equal(t, tc.window, invalid.B)
					_, recordErr := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
					require.ErrorAs(t, recordErr, &invalid, "recorded override presence must reproduce strict validation")
					return
				}
				require.NoError(t, err)
				record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
				require.NoError(t, err)
				diagnostics := 0
				for _, event := range record.Runs[0].Events {
					if event.Type == hooks.ContextBudgetTooSmall {
						diagnostics++
					}
				}
				assert.Equal(t, tc.diagnostics, diagnostics)
			})
		}
	}
}
