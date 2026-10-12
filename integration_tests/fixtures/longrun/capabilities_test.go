package longrun_test

import (
	"testing"

	"example.com/longrun"
	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfflineClientsSupportAttachments(t *testing.T) {
	cfg, err := longrun.ProviderConfiguration("offline", fake.Faults{})
	require.NoError(t, err)
	for _, name := range []string{"planner", "planner_small", "summariser"} {
		t.Run(name, func(t *testing.T) {
			factory := cfg.Planner
			if name == "planner_small" {
				factory = cfg.PlannerSmall
			} else if name == "summariser" {
				factory = cfg.Summariser
			}
			client, err := factory(t.Context())
			require.NoError(t, err)
			info, ok := client.(model.ToolResultPartsInfo)
			require.True(t, ok, "registered client must preserve attachment support")
			support := info.ToolResultParts(t.Context(), &model.Request{})
			assert.Equal(t, []model.ImageFormat{"png"}, support.ImageFormats)
			assert.Equal(t, []model.DocumentFormat{"pdf"}, support.DocumentFormats)
			assert.Positive(t, support.MaxImageBytes)
			assert.Positive(t, support.MaxDocumentBytes)
		})
	}
}
