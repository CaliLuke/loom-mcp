package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterModelClientReturnsInstalledIdentity(t *testing.T) {
	rt := New(WithContextLimits(map[string]model.ContextLimits{"model": {ContextWindow: 4096}}))
	installed, err := rt.RegisterModelClient("model", &contractModelClient{})
	require.NoError(t, err)
	current, ok := rt.ModelClient("model")
	require.True(t, ok)
	if installed != current {
		t.Fatal("registration did not return the installed wrapper")
	}
	require.NoError(t, rt.RegisterModel("model", &contractModelClient{}))
	current, ok = rt.ModelClient("model")
	require.True(t, ok)
	if installed == current {
		t.Error("replacement retained the previous registration identity")
	}
	require.NoError(t, rt.Seal(t.Context()))
	rejected, err := rt.RegisterModelClient("model", &contractModelClient{})
	require.ErrorIs(t, err, ErrRegistrationClosed)
	assert.Nil(t, rejected)
}
