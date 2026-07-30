package codegen_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	gcodegen "github.com/CaliLuke/loom/codegen"
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

	content := generatedInjectContent(t, files)
	require.Contains(t, content, `func InjectByHousehold(payload *ByHouseholdPayload, meta runtime.ToolCallMeta, labels map[string]string) error`)
	require.Contains(t, content, `labelValue0, ok := labels["household_id"]`)
	require.Contains(t, content, `missing required run label %q`)
	require.Contains(t, content, `payload.HouseholdID = &labelValue0`)
	require.Contains(t, content, `func DecodeByHousehold(data []byte, meta runtime.ToolCallMeta, labels map[string]string) (*ByHouseholdPayload, error)`)
}

func TestInjectMultipleLabelBackedFieldsUsesDistinctLocals(t *testing.T) {
	files := testhelpers.BuildAndGenerate(t, func() {
		goadsl.API("svc", func() {})
		goadsl.Service("svc", func() {
			Agent("scribe", "Doc helper", func() {
				Use("lookup", func() {
					Tool("by_scope", "Lookup by scope", func() {
						Args(func() {
							goadsl.Attribute("household_id", goadsl.String)
							goadsl.Attribute("account_id", goadsl.String)
							goadsl.Attribute("query", goadsl.String)
							goadsl.Required("household_id", "account_id", "query")
						})
						Return(goadsl.String)
						Inject("household_id", "account_id")
					})
				})
			})
		})
	})

	content := generatedInjectContent(t, files)
	_, err := parser.ParseFile(token.NewFileSet(), "inject.go", content, parser.AllErrors)
	require.NoError(t, err)
	require.Contains(t, content, `labelValue0, ok := labels["household_id"]`)
	require.Contains(t, content, `payload.HouseholdID = &labelValue0`)
	require.Contains(t, content, `labelValue1, ok := labels["account_id"]`)
	require.Contains(t, content, `payload.AccountID = &labelValue1`)
	require.NotContains(t, content, `v, ok := labels`)
}

func generatedInjectContent(t *testing.T, files []*gcodegen.File) string {
	t.Helper()

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
	return content
}
