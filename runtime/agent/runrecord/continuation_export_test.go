package runrecord_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContinuationExportOptionalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		pos          int
		prev         *string
	}{
		{"protocol15", `"workflow_protocol_version":15`, 0, nil},
		{"protocol17", `"workflow_protocol_version":17,"pos":2,"prev":"earlier"`, 2, new("earlier")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(`{"version":1,"root_run_id":"run","runs":[{"run_id":"run",` + tc.fields + `}],"content":{}}`)
			record, err := runrecord.FromExport(data)
			require.NoError(t, err)
			require.Len(t, record.Runs, 1)
			assert.Equal(t, tc.pos, record.Runs[0].Pos)
			assert.Equal(t, tc.prev, record.Runs[0].Prev)
			encoded, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(encoded)
			require.NoError(t, err)
			assert.Equal(t, record, restored)
		})
	}
}
