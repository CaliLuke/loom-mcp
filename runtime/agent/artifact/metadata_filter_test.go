package artifact

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStoreMetadataFilterRequiresKey(t *testing.T) {
	for _, tt := range []struct {
		name     string
		metadata map[string]string
		filter   map[string]string
		matches  int
	}{
		{name: "nil metadata", filter: map[string]string{"kind": ""}},
		{name: "missing key", metadata: map[string]string{"other": ""}, filter: map[string]string{"kind": ""}},
		{name: "explicit empty", metadata: map[string]string{"kind": ""}, filter: map[string]string{"kind": ""}, matches: 1},
		{name: "matching value", metadata: map[string]string{"kind": "report"}, filter: map[string]string{"kind": "report"}, matches: 1},
		{name: "different value", metadata: map[string]string{"kind": "report"}, filter: map[string]string{"kind": ""}},
		{name: "all keys required", metadata: map[string]string{"kind": "report"}, filter: map[string]string{"kind": "report", "missing": ""}},
		{name: "no filter", matches: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			_, err := store.Save(t.Context(), SaveInput{AgentID: "agent", RunID: "run", Metadata: tt.metadata})
			require.NoError(t, err)
			refs, err := store.List(t.Context(), ListQuery{AgentID: "agent", RunID: "run", Metadata: tt.filter})
			require.NoError(t, err)
			assert.Len(t, refs, tt.matches)
		})
	}
}
