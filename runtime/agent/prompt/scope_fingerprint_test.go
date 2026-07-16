package prompt

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScopeFingerprintIsCanonicalAndUnambiguous(t *testing.T) {
	t.Parallel()

	left := map[string]string{"region": "west", "account": "acme"}
	right := map[string]string{"account": "acme", "region": "west"}
	require.Equal(t, ScopeFingerprint(left), ScopeFingerprint(right))
	require.NotEqual(t,
		ScopeFingerprint(map[string]string{"a": "b=c"}),
		ScopeFingerprint(map[string]string{"a=b": "c"}),
	)
	require.NotEmpty(t, ScopeFingerprint(nil))
}
