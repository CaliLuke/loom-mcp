package gate

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestFreezeDrift(t *testing.T) {
	f := Freeze{Format: FreezeFormat, Version: 1, Configuration: "offline", Margins: DefaultMargins(), Alpha: .05, Looks: []int{12, 24, 48}, Repeats: 2, Bootstrap: Bootstrap{10000, 42}, Guard: -.10, GatedScenarios: []string{"a"}, MaterializerVersion: "v1", SimulatorVersion: SimulatorVersion, Created: time.Unix(0, 0), Pools: []Pool{{Scenario: "a", Partition: "look1", DatasetHash: Digest([]byte("pool")), Items: []FrozenItem{{ID: "one", SHA256: Digest([]byte("materialized"))}}}}}
	hash, err := f.Hash()
	require.NoError(t, err)
	inputs := FreezeInputs{Pools: map[string][]byte{"a/look1": []byte("pool")}, Materialized: map[string][]byte{"one": []byte("materialized")}, MaterializerVersion: "v1"}
	require.NoError(t, f.Verify(hash, inputs))
	require.Error(t, CanGenerateItems(&f))
	inputs.Pools["a/look1"] = []byte("drift")
	require.Error(t, f.Verify(hash, inputs))
	inputs.Pools["a/look1"] = []byte("pool")
	inputs.Materialized["one"] = []byte("drift")
	require.Error(t, f.Verify(hash, inputs))
	inputs.Materialized["one"] = []byte("materialized")
	f.Margins.Success = -.06
	require.Error(t, f.Verify(hash, inputs))
	f.Margins = DefaultMargins()
	f.Looks[1] = 25
	require.Error(t, f.Verify(hash, inputs))
}
