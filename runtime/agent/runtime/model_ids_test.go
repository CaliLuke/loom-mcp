package runtime

import (
	"context"
	"testing"

	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelIDsSortedOwnedAndStable(t *testing.T) {
	rt := New(WithEngine(engineinmem.New()))
	assert.Empty(t, rt.ModelIDs())
	client := &callInfoClient{}
	require.NoError(t, rt.RegisterModel("z", client))
	require.NoError(t, rt.RegisterModel("a", client))
	require.NoError(t, rt.RegisterModel("middle", client))
	ids := rt.ModelIDs()
	assert.Equal(t, []string{"a", "middle", "z"}, ids)
	ids[0] = "changed"
	assert.Equal(t, []string{"a", "middle", "z"}, rt.ModelIDs())
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return oracleFinal(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("model-ids"))
	require.NoError(t, err)
	require.ErrorIs(t, rt.RegisterModel("later", client), ErrRegistrationClosed)
	assert.Equal(t, []string{"a", "middle", "z"}, rt.ModelIDs())
}
