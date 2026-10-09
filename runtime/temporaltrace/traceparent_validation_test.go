package temporaltrace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTraceParentWireValidation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		header  string
		valid   bool
		sampled bool
	}{
		{name: "sampled", header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", valid: true, sampled: true},
		{name: "unsampled", header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00", valid: true},
		{name: "nonhex version", header: "zz-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{name: "uppercase trace", header: "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01"},
		{name: "uppercase span", header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00F067AA0BA902B7-01"},
		{name: "reserved flags", header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-ff"},
		{name: "zero trace", header: "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
		{name: "zero span", header: "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTraceParent(tt.header)
			if !tt.valid {
				require.Error(t, err)
				assert.False(t, got.IsValid())
				return
			}
			require.NoError(t, err)
			assert.True(t, got.IsRemote())
			assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", got.TraceID().String())
			assert.Equal(t, "00f067aa0ba902b7", got.SpanID().String())
			assert.Equal(t, tt.sampled, got.IsSampled())
		})
	}
}
