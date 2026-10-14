package eval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cExactSelectionWireModes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection Selection
		wantKeys  string
	}{
		{"legacy", Selection{}, ""},
		{"empty exact", Selection{Keys: []AttemptKey{}}, `"keys":[]`},
		{"sparse", Selection{Keys: []AttemptKey{{Variant: "v", Scenario: "s", Item: "i", Repeat: 2}}}, `"keys":[`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := encodeStore(tc.selection)
			require.NoError(t, err)
			if tc.wantKeys == "" {
				assert.NotContains(t, string(data), `"keys"`)
			} else {
				assert.Contains(t, string(data), tc.wantKeys)
			}
			var decoded Selection
			require.NoError(t, decodeStore(data, &decoded))
			assert.Equal(t, tc.selection.Keys, decoded.Keys)
		})
	}
	var old Selection
	require.NoError(t, decodeStore([]byte(`{"variants":[],"scenarios":[],"tags":[],"items":[]}`), &old))
	assert.Nil(t, old.Keys)
	require.Error(t, decodeStore([]byte(`{"keys":[],"surprise":1}`), &old))
	require.Error(t, decodeStore([]byte(`{"keys":[{"Variant":"v","Scenario":"s","Item":"i","Repeat":2,"extra":1}]}`), &old))
}
