package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type metadataOnlySessionStore struct{ session.Store }

func TestContinuationRegistrationRequiresStore(t *testing.T) {
	rt := New()
	rt.SessionStore = metadataOnlySessionStore{sessioninmem.New()}
	reg := AgentRegistration{ID: "agent"}
	reg.Policy.ContextCompaction = &ContextCompaction{}
	require.ErrorContains(t, rt.validateRecallRegistration(reg), "ContinuationStore")
	reg.Policy.ContextCompaction = nil
	assert.NoError(t, rt.validateRecallRegistration(reg))
}
