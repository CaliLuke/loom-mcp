package codegen_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/dsl"
	goadsl "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

func TestInjectLabelBackedFieldPopulatesFromRunLabels(t *testing.T) {
	files := testhelpers.BuildAndGenerate(t, func() {
		goadsl.API("svc", func() {})
		goadsl.Service("svc", func() {
			Agent("scribe", "Doc helper", func() {
				Use("lookup", func() {
					Tool("by_household", "Lookup by household", func() {
						Args(func() {
							goadsl.Attribute("household_id", goadsl.String)
							goadsl.Attribute("query", goadsl.String)
							goadsl.Required("household_id", "query")
						})
						Return(goadsl.String)
						Inject("household_id")
					})
				})
			})
		})
	})

	var content string
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
		if strings.HasSuffix(f.Path, "/inject.go") {
			var buf bytes.Buffer
			for _, section := range f.AllSections() {
				require.NoError(t, section.Write(&buf))
			}
			content = buf.String()
		}
	}
	require.NotEmpty(t, content, "generated paths: %s", strings.Join(paths, "\n"))
	require.Contains(t, content, `func InjectByHousehold(payload *ByHouseholdPayload, meta runtime.ToolCallMeta, labels map[string]string) error`)
	require.Contains(t, content, `v, ok := labels["household_id"]`)
	require.Contains(t, content, `missing required run label %q`)
	require.Contains(t, content, `payload.HouseholdID = &v`)
	require.Contains(t, content, `func DecodeByHousehold(data []byte, meta runtime.ToolCallMeta, labels map[string]string) (*ByHouseholdPayload, error)`)
}
