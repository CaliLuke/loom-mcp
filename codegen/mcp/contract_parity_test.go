package codegen_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"text/template"

	agentcodegen "github.com/CaliLuke/loom-mcp/codegen/agent"
	mcpcodegen "github.com/CaliLuke/loom-mcp/codegen/mcp"
	"github.com/CaliLuke/loom-mcp/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/dsl"
	agentsExpr "github.com/CaliLuke/loom-mcp/expr/agent"
	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
	gcodegen "github.com/CaliLuke/loom/codegen"
	goadsl "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	openapi "github.com/CaliLuke/loom/http/codegen/openapi"
	"github.com/stretchr/testify/require"
)

type unionContractFacts struct {
	TopLevelRequired   []string
	WrapperDescription string
	Discriminator      string
	Tags               []string
}

type fieldContractFacts struct {
	TopLevelRequired []string
	ModeDefault      string
	ModeExamples     []string
}

type boundedResultContractFacts struct {
	Required []string
	Fields   []string
}

func TestUnionContractParityAcrossGeneratedSurfaces(t *testing.T) {
	attr := runUnionContractParityDesign(t)

	inlineFacts := extractUnionContractFacts(t, mustInlineJSONSchema(t, attr))
	openAPIFacts := extractUnionContractFacts(t, mustOpenAPISchemaJSON(t, attr))
	mcpFacts := extractUnionContractFacts(t, mustMCPInputSchema(t))
	agentFacts := extractUnionContractFacts(t, mustAgentToolSchema(t))

	require.Equal(t, inlineFacts, openAPIFacts)
	require.Equal(t, inlineFacts, mcpFacts)
	require.Equal(t, inlineFacts, agentFacts)
	require.Equal(t, []string{"request"}, inlineFacts.TopLevelRequired)
	require.Equal(t, "action", inlineFacts.Discriminator)
	require.Equal(t, "Action envelope", inlineFacts.WrapperDescription)
	require.Equal(t, []string{"get_active", "list"}, inlineFacts.Tags)
}

func TestFieldContractParityAcrossGeneratedSurfaces(t *testing.T) {
	attr := runFieldContractParityDesign(t)

	inlineFacts := extractFieldContractFacts(t, mustInlineJSONSchema(t, attr))
	openAPIFacts := extractFieldContractFacts(t, mustOpenAPISchemaJSON(t, attr))
	mcpFacts := extractFieldContractFacts(t, mustMCPInputSchema(t))
	agentFacts := extractFieldContractFacts(t, mustAgentPayloadSchema(t))

	require.Equal(t, inlineFacts, openAPIFacts)
	require.Equal(t, inlineFacts, mcpFacts)
	require.Equal(t, inlineFacts, agentFacts)
	require.Equal(t, []string{"id"}, inlineFacts.TopLevelRequired)
	require.Equal(t, "fast", inlineFacts.ModeDefault)
	require.Equal(t, []string{"safe"}, inlineFacts.ModeExamples)
}

func TestBoundedResultProjectionContract(t *testing.T) {
	resultAttr := runBoundedResultParityDesign(t)

	semanticFacts := extractBoundedResultContractFacts(t, mustInlineJSONSchema(t, resultAttr))
	agentFacts := extractBoundedResultContractFacts(t, mustAgentResultSchema(t))

	require.Equal(t, []string{"results"}, semanticFacts.Required)
	require.Equal(t, []string{"results"}, semanticFacts.Fields)
	require.Equal(t, []string{"results", "returned", "truncated"}, agentFacts.Required)
	require.Equal(t, []string{"next_cursor", "refinement_hint", "results", "returned", "total", "truncated"}, agentFacts.Fields)
}

func runUnionContractParityDesign(t *testing.T) *expr.AttributeExpr {
	t.Helper()

	setupParityRoots(t)

	var requestType expr.UserType
	design := func() {
		goadsl.API("alpha", func() {
			goadsl.Server("default", func() {
				goadsl.Host("local", func() {
					goadsl.URI("http://localhost:8080")
				})
			})
		})

		listPayload := goadsl.Type("ListPayload", func() {
			goadsl.Attribute("limit", goadsl.Int, "Maximum number of items")
		})
		getActivePayload := goadsl.Type("GetActivePayload", func() {
		})
		requestType = goadsl.Type("Request", func() {
			goadsl.OneOf("request", "Action envelope", func() {
				goadsl.Meta("oneof:type:field", "action")
				goadsl.Meta("oneof:value:field", "value")
				goadsl.Attribute("list_branch", listPayload, func() {
					goadsl.Meta("oneof:type:tag", "list")
				})
				goadsl.Attribute("get_active_branch", getActivePayload, func() {
					goadsl.Meta("oneof:type:tag", "get_active")
				})
			})
			goadsl.Required("request")
		})

		goadsl.Service("alpha", func() {
			MCP("alpha-mcp", "1.0.0")
			goadsl.JSONRPC(func() {
				goadsl.POST("/rpc")
			})

			goadsl.Method("dispatch", func() {
				goadsl.Payload(requestType)
				goadsl.Result(goadsl.String)
				Tool("dispatch", "Dispatch through MCP")
				goadsl.JSONRPC(func() {})
			})

			Agent("scribe", "Doc helper", func() {
				Use("ops", func() {
					Tool("dispatch_local", "Dispatch locally", func() {
						Args(requestType)
						Return(goadsl.String)
					})
				})
			})
		})
	}

	ok := eval.Execute(design, nil)
	require.True(t, ok, eval.Context.Error())
	require.NoError(t, eval.RunDSL())

	return requestType.Attribute()
}

func runFieldContractParityDesign(t *testing.T) *expr.AttributeExpr {
	t.Helper()

	setupParityRoots(t)

	var requestType expr.UserType
	design := func() {
		goadsl.API("alpha", func() {
			goadsl.Server("default", func() {
				goadsl.Host("local", func() {
					goadsl.URI("http://localhost:8080")
				})
			})
		})

		requestType = goadsl.Type("Request", func() {
			goadsl.Attribute("id", goadsl.String, "Stable identifier")
			goadsl.Attribute("mode", goadsl.String, "Execution mode", func() {
				goadsl.Default("fast")
				goadsl.Example("safe")
			})
			goadsl.Attribute("note", goadsl.String, "Optional note")
			goadsl.Required("id")
		})

		goadsl.Service("alpha", func() {
			MCP("alpha-mcp", "1.0.0")
			goadsl.JSONRPC(func() {
				goadsl.POST("/rpc")
			})

			goadsl.Method("dispatch", func() {
				goadsl.Payload(requestType)
				goadsl.Result(goadsl.String)
				Tool("dispatch", "Dispatch through MCP")
				goadsl.JSONRPC(func() {})
			})

			Agent("scribe", "Doc helper", func() {
				Use("ops", func() {
					Tool("dispatch_local", "Dispatch locally", func() {
						Args(requestType)
						Return(goadsl.String)
					})
				})
			})
		})
	}

	ok := eval.Execute(design, nil)
	require.True(t, ok, eval.Context.Error())
	require.NoError(t, eval.RunDSL())

	return requestType.Attribute()
}

func runBoundedResultParityDesign(t *testing.T) *expr.AttributeExpr {
	t.Helper()

	setupParityRoots(t)

	var resultType expr.UserType
	design := func() {
		goadsl.API("alpha", func() {})

		resultType = goadsl.Type("SearchResult", func() {
			goadsl.Attribute("results", goadsl.ArrayOf(goadsl.String))
			goadsl.Required("results")
		})

		goadsl.Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use("ops", func() {
					Tool("search_local", "Search locally", func() {
						Args(func() {
							goadsl.Attribute("query", goadsl.String)
							goadsl.Attribute("cursor", goadsl.String)
							goadsl.Required("query")
						})
						Return(resultType)
						BoundedResult(func() {
							Cursor("cursor")
							NextCursor("next_cursor")
						})
					})
				})
			})
		})
	}

	ok := eval.Execute(design, nil)
	require.True(t, ok, eval.Context.Error())
	require.NoError(t, eval.RunDSL())

	return resultType.Attribute()
}

func setupParityRoots(t *testing.T) {
	t.Helper()

	eval.Reset()
	expr.Root = new(expr.RootExpr)
	expr.GeneratedResultTypes = new(expr.ResultTypesRoot)
	mcpexpr.Root = mcpexpr.NewRoot()
	agentsExpr.Root = &agentsExpr.RootExpr{}

	require.NoError(t, eval.Register(expr.Root))
	require.NoError(t, eval.Register(expr.GeneratedResultTypes))
	require.NoError(t, eval.Register(mcpexpr.Root))
	require.NoError(t, eval.Register(agentsExpr.Root))
}

func mustInlineJSONSchema(t *testing.T, attr *expr.AttributeExpr) []byte {
	t.Helper()

	schema, err := expr.InlineJSONSchema(attr)
	require.NoError(t, err)
	return schema
}

func mustOpenAPISchemaJSON(t *testing.T, attr *expr.AttributeExpr) []byte {
	t.Helper()

	openapi.Definitions = make(map[string]*openapi.Schema)
	schema := openapi.AttributeTypeSchema(expr.Root.API, attr)
	payload, err := json.Marshal(schema)
	require.NoError(t, err)
	return payload
}

func mustMCPInputSchema(t *testing.T) []byte {
	t.Helper()

	files, err := mcpcodegen.Generate("example.com/parity", []eval.Root{expr.Root, expr.GeneratedResultTypes, mcpexpr.Root}, nil)
	require.NoError(t, err)

	registerSource := renderedFileBySuffix(t, files, "register.go")
	require.Contains(t, registerSource, `dispatch`)

	re := regexp.MustCompile(`Schema:\s*\[\]byte\("((?:\\.|[^"])*)"\)`)
	match := re.FindStringSubmatch(registerSource)
	require.Len(t, match, 2, "expected rendered register.go to embed a tool schema")

	var schema string
	require.NoError(t, json.Unmarshal([]byte(`"`+match[1]+`"`), &schema))
	return []byte(schema)
}

func mustAgentToolSchema(t *testing.T) []byte {
	t.Helper()

	tool := mustAgentToolEntry(t)
	return tool.Payload.Schema
}

func mustAgentPayloadSchema(t *testing.T) []byte {
	t.Helper()

	tool := mustAgentToolEntry(t)
	return tool.Payload.Schema
}

func mustAgentResultSchema(t *testing.T) []byte {
	t.Helper()

	tool := mustAgentToolEntry(t)
	return tool.Result.Schema
}

func mustAgentToolEntry(t *testing.T) agentToolSchemaDoc {
	t.Helper()

	files, err := agentcodegen.Generate("example.com/parity", []eval.Root{expr.Root, expr.GeneratedResultTypes, agentsExpr.Root}, nil)
	require.NoError(t, err)

	var doc struct {
		Tools []agentToolSchemaDoc `json:"tools"`
	}
	payload := testhelpers.FileContent(t, files, filepath.ToSlash("gen/alpha/agents/scribe/specs/tool_schemas.json"))
	require.NoError(t, json.Unmarshal([]byte(payload), &doc))

	for _, tool := range doc.Tools {
		if tool.ID == "ops.dispatch_local" || tool.ID == "ops.search_local" {
			return tool
		}
	}

	require.Fail(t, "agent tool schema not found")
	return agentToolSchemaDoc{}
}

func extractUnionContractFacts(t *testing.T, schemaJSON []byte) unionContractFacts {
	t.Helper()

	var schema map[string]any
	require.NoError(t, json.Unmarshal(schemaJSON, &schema))

	required := stringSliceFromAny(t, schema["required"])
	slices.Sort(required)

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	request, ok := properties["request"].(map[string]any)
	require.True(t, ok)

	discriminator := ""
	if raw, ok := request["discriminator"].(map[string]any); ok {
		discriminator, _ = raw["propertyName"].(string)
	}

	oneOf, ok := request["oneOf"].([]any)
	require.True(t, ok)
	tags := make([]string, 0, len(oneOf))
	for _, raw := range oneOf {
		branch, ok := raw.(map[string]any)
		require.True(t, ok)
		branchProperties, ok := branch["properties"].(map[string]any)
		require.True(t, ok)
		action, ok := branchProperties["action"].(map[string]any)
		require.True(t, ok)
		enumValues := stringSliceFromAny(t, action["enum"])
		require.Len(t, enumValues, 1)
		tags = append(tags, enumValues[0])
	}
	slices.Sort(tags)

	description, _ := request["description"].(string)
	return unionContractFacts{
		TopLevelRequired:   required,
		WrapperDescription: description,
		Discriminator:      discriminator,
		Tags:               tags,
	}
}

func extractFieldContractFacts(t *testing.T, schemaJSON []byte) fieldContractFacts {
	t.Helper()

	var schema map[string]any
	require.NoError(t, json.Unmarshal(schemaJSON, &schema))

	required := stringSliceFromAny(t, schema["required"])
	slices.Sort(required)

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	mode, ok := properties["mode"].(map[string]any)
	require.True(t, ok)

	examples := normalizeExamples(t, mode)
	slices.Sort(examples)

	modeDefault, _ := mode["default"].(string)
	return fieldContractFacts{
		TopLevelRequired: required,
		ModeDefault:      modeDefault,
		ModeExamples:     examples,
	}
}

func extractBoundedResultContractFacts(t *testing.T, schemaJSON []byte) boundedResultContractFacts {
	t.Helper()

	var schema map[string]any
	require.NoError(t, json.Unmarshal(schemaJSON, &schema))

	required := stringSliceFromAny(t, schema["required"])
	slices.Sort(required)

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	fields := make([]string, 0, len(properties))
	for name := range properties {
		fields = append(fields, name)
	}
	slices.Sort(fields)

	return boundedResultContractFacts{
		Required: required,
		Fields:   fields,
	}
}

func stringSliceFromAny(t *testing.T, raw any) []string {
	t.Helper()

	if raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	require.True(t, ok)
	out := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		require.True(t, ok)
		out = append(out, value)
	}
	return out
}

func normalizeExamples(t *testing.T, schema map[string]any) []string {
	t.Helper()

	if raw, ok := schema["examples"]; ok {
		return stringSliceFromAny(t, raw)
	}
	if raw, ok := schema["example"]; ok {
		value, ok := raw.(string)
		require.True(t, ok)
		return []string{value}
	}
	return nil
}

type agentToolSchemaDoc struct {
	ID      string `json:"id"`
	Payload struct {
		Schema json.RawMessage `json:"schema"`
	} `json:"payload"`
	Result struct {
		Schema json.RawMessage `json:"schema"`
	} `json:"result"`
}

func renderedFileBySuffix(t *testing.T, files []*gcodegen.File, suffix string) string {
	t.Helper()

	for _, file := range files {
		if filepath.Base(file.Path) != suffix {
			continue
		}
		return renderGeneratedFile(t, file)
	}

	require.Failf(t, "generated file not found", "suffix %q", suffix)
	return ""
}

func renderGeneratedFile(t *testing.T, file *gcodegen.File) string {
	t.Helper()

	var output bytes.Buffer
	for _, section := range file.AllSections() {
		switch sec := section.(type) {
		case *gcodegen.SectionTemplate:
			tmpl := template.New(sec.Name).Funcs(template.FuncMap{
				"comment": gcodegen.Comment,
				"commandLine": func() string {
					return ""
				},
			})
			if sec.FuncMap != nil {
				tmpl = tmpl.Funcs(sec.FuncMap)
			}
			parsed, err := tmpl.Parse(sec.Source)
			require.NoError(t, err)

			var rendered bytes.Buffer
			err = parsed.Execute(&rendered, sec.Data)
			require.NoError(t, err)
			output.Write(rendered.Bytes())
		default:
			err := section.Write(&output)
			require.NoError(t, err, "render %s", section.SectionName())
		}
	}

	require.NotEmpty(t, output.String(), filepath.ToSlash(file.Path))
	return output.String()
}
