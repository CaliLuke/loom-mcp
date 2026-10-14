package gate

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFreezeDrift(t *testing.T) {
	f, inputs := v2FreezeFixture()
	hash, err := f.Hash()
	require.NoError(t, err)
	require.NoError(t, f.Verify(hash, inputs))
	require.Error(t, CanGenerateItems(&f))
	inputs.Pools["a/look1"] = []byte("drift")
	require.Error(t, f.Verify(hash, inputs))
	inputs.Pools["a/look1"] = []byte("pool")
	inputs.Materialized["one"] = []byte("drift")
	require.Error(t, f.Verify(hash, inputs))
	inputs.Materialized["one"] = []byte("item")
	f.Policy.Success = -.06
	require.Error(t, f.Verify(hash, inputs))
	f.Policy.Success = -.05
	f.Policy.Looks[1] = 25
	require.Error(t, f.Verify(hash, inputs))
}
