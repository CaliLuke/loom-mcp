package trajectory_test

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraceIDDocumentAndRender(t *testing.T) {
	for _, id := range []string{"", "1234567890abcdef1234567890abcdef"} {
		for _, format := range []trajectory.Format{trajectory.FormatText, trajectory.FormatMarkdown} {
			t.Run(string(format)+id, func(t *testing.T) {
				tr := renderFixtures(t)["tree"][0]
				tr.TraceID = id
				encoded, err := tr.Marshal()
				require.NoError(t, err)
				document, err := trajectory.EncodeDocument(tr)
				require.NoError(t, err)
				require.NoError(t, compileTrajectorySchema(t).Validate(schemaValue(t, document)))
				rendered := tr.Render(trajectory.RenderOptions{Format: format})
				if id == "" {
					assert.NotContains(t, string(encoded), "trace_id")
					assert.NotContains(t, string(document), "trace_id")
					assert.NotContains(t, rendered, "Trace:")
				} else {
					assert.Contains(t, string(encoded), `"trace_id":"`+id+`"`)
					assert.Contains(t, string(document), `"trace_id":"`+id+`"`)
					assert.Contains(t, rendered, "Trace: "+id)
					assert.Less(t, strings.Index(rendered, "Agent:"), strings.Index(rendered, "Trace:"))
					assert.Less(t, strings.Index(rendered, "Trace:"), strings.Index(rendered, "Status:"))
				}
			})
		}
	}
}
