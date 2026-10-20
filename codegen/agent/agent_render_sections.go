package codegen

import (
	"fmt"
	"strings"

	gocodegen "github.com/CaliLuke/loom/codegen"
)

func toolTypesSection(data toolTypesFileData) gocodegen.Section {
	return gocodegen.NewRenderSection("tool-spec-types", func() string {
		var b strings.Builder
		b.WriteString("type (\n")
		wrote := false
		for _, t := range data.Types {
			if t == nil {
				continue
			}
			if wrote {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "\t// %s\n", t.Doc)
			fmt.Fprintf(&b, "\t%s\n", t.Def)
			wrote = true
		}
		b.WriteString(")\n")
		return b.String()
	})
}

func toolTransportTypesSection(data toolTransportTypesFileData) gocodegen.Section {
	return gocodegen.NewRenderSection("tool-transport-types", func() string {
		var b strings.Builder
		b.WriteString("type (\n")
		wrote := false
		for _, t := range data.Types {
			if t == nil || t.TransportDef == "" {
				continue
			}
			if wrote {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "\t// %s is the internal JSON transport type for %s.\n", t.TransportTypeName, t.TypeName)
			b.WriteString("\t// It lives in the toolset-local http package and is used only for JSON\n")
			b.WriteString("\t// decode + validation (missing-field detection) before transforming into\n")
			b.WriteString("\t// the public tool type.\n")
			fmt.Fprintf(&b, "\t%s\n", t.TransportDef)
			wrote = true
		}
		b.WriteString(")\n")
		return b.String()
	})
}

func toolTransportValidateSection(data toolTransportTypesFileData) gocodegen.Section {
	return gocodegen.NewRenderSection("tool-transport-validate", func() string {
		var b strings.Builder
		for _, t := range data.Types {
			if t == nil || len(t.TransportValidationSrc) == 0 {
				continue
			}
			fmt.Fprintf(&b, "// Validate%s runs the validations defined on %s.\n", t.TransportTypeName, t.TransportTypeName)
			b.WriteString(t.TransportValidator.Definition(strings.Join(t.TransportValidationSrc, "\n")))
			b.WriteString("\n")
		}
		return b.String()
	})
}

func toolTransformsSection(data transformsFileData) gocodegen.Section {
	return gocodegen.NewRenderSection("tool-transforms", func() string {
		var b strings.Builder
		for _, fn := range data.Functions {
			fmt.Fprintf(&b, "// %s converts %s to %s.\n", fn.Name, fn.ParamTypeRef, fn.ResultTypeRef)
			if fn.ErrorAware {
				fmt.Fprintf(&b, "func %s(in %s) (%s, error) {\n", fn.Name, fn.ParamTypeRef, fn.ResultTypeRef)
			} else {
				fmt.Fprintf(&b, "func %s(in %s) %s {\n", fn.Name, fn.ParamTypeRef, fn.ResultTypeRef)
			}
			if fn.NilInputReturnsNil {
				b.WriteString("\tif in == nil {\n")
				if fn.ErrorAware {
					b.WriteString("\t\treturn nil, nil\n")
				} else {
					b.WriteString("\t\treturn nil\n")
				}
				b.WriteString("\t}\n")
			}
			fmt.Fprintf(&b, "\tvar out %s\n", fn.ResultTypeRef)
			if fn.ErrorAware {
				b.WriteString("\tvar conversionErr error\n\ttransformErr := &conversionErr\n")
			}
			writeIndentedBlock(&b, fn.Body, 1)
			if fn.ErrorAware {
				fmt.Fprintf(&b, "\tif conversionErr != nil {\n\t\tvar zero %s\n\t\treturn zero, conversionErr\n\t}\n\treturn out, nil\n", fn.ResultTypeRef)
			} else {
				b.WriteString("\treturn out\n")
			}
			b.WriteString("}\n\n")
		}
		b.WriteString("// Helper transform functions\n")
		for _, helper := range data.Helpers {
			if helper == nil {
				continue
			}
			if helper.ErrorAware {
				fmt.Fprintf(&b, "func %s(v %s, transformErr *error) %s {\n", helper.Name, helper.ParamTypeRef, helper.ResultTypeRef)
			} else {
				fmt.Fprintf(&b, "func %s(v %s) %s {\n", helper.Name, helper.ParamTypeRef, helper.ResultTypeRef)
			}
			writeIndentedBlock(&b, helper.Code, 1)
			b.WriteString("\treturn res\n")
			b.WriteString("}\n\n")
		}
		return b.String()
	})
}

func toolSpecsSection(data toolSpecFileData) gocodegen.Section {
	return gocodegen.NewRenderSection("tool-specs", func() string {
		var b strings.Builder
		b.WriteString("// Tool IDs for this toolset.\nconst (\n")
		for _, tool := range data.Tools {
			fmt.Fprintf(&b, "\t%s tools.Ident = %q\n", tool.ConstName, tool.Name)
		}
		b.WriteString(")\n\n")
		b.WriteString("var Specs = []tools.ToolSpec{\n")
		for _, tool := range data.Tools {
			fmt.Fprintf(&b, "\tSpec%s,\n", tool.ConstName)
		}
		b.WriteString("}\n\nvar (\n")
		for _, tool := range data.Tools {
			fmt.Fprintf(&b, "\tSpec%s = tools.ToolSpec{\n", tool.ConstName)
			fmt.Fprintf(&b, "\t\tName:        %s,\n", tool.ConstName)
			fmt.Fprintf(&b, "\t\tService:     %q,\n", tool.Service)
			fmt.Fprintf(&b, "\t\tToolset:     %q,\n", tool.Toolset)
			fmt.Fprintf(&b, "\t\tDescription: %q,\n", tool.Description)
			b.WriteString("\t\tTags: []string{\n")
			for _, tag := range tool.Tags {
				fmt.Fprintf(&b, "\t\t\t%q,\n", tag)
			}
			b.WriteString("\t\t},\n")
			if len(tool.MetaPairs) > 0 {
				b.WriteString("\t\tMeta: map[string][]string{\n")
				for _, pair := range tool.MetaPairs {
					fmt.Fprintf(&b, "\t\t\t%q: []string{\n", pair.Key)
					for _, value := range pair.Values {
						fmt.Fprintf(&b, "\t\t\t\t%q,\n", value)
					}
					b.WriteString("\t\t\t},\n")
				}
				b.WriteString("\t\t},\n")
			}
			if tool.IsExportedByAgent {
				b.WriteString("\t\tIsAgentTool: true,\n")
				fmt.Fprintf(&b, "\t\tAgentID:     %q,\n", tool.ExportingAgentID)
			}
			if tool.TerminalRun {
				b.WriteString("\t\tTerminalRun: true,\n")
			}
			if tool.Bookkeeping {
				b.WriteString("\t\tBookkeeping: true,\n")
			}

			if tool.KeepResultInContext {
				b.WriteString("\t\tKeepResultInContext: true,\n")
			}

			if tool.Bounds != nil {
				b.WriteString("\t\tBounds: &tools.BoundsSpec{\n")
				if tool.Bounds.Paging != nil {
					b.WriteString("\t\t\tPaging: &tools.PagingSpec{\n")
					fmt.Fprintf(&b, "\t\t\t\tCursorField: %q,\n", tool.Bounds.Paging.CursorField)
					fmt.Fprintf(&b, "\t\t\t\tNextCursorField: %q,\n", tool.Bounds.Paging.NextCursorField)
					b.WriteString("\t\t\t},\n")
				}
				b.WriteString("\t\t},\n")
			}
			if len(tool.ServerData) > 0 {
				b.WriteString("\t\tServerData: []*tools.ServerDataSpec{\n")
				for _, sd := range tool.ServerData {
					b.WriteString("\t\t\t{\n")
					fmt.Fprintf(&b, "\t\t\t\tKind: %q,\n", sd.Kind)
					fmt.Fprintf(&b, "\t\t\t\tAudience: tools.ServerDataAudience(%q),\n", sd.Audience)
					fmt.Fprintf(&b, "\t\t\t\tDescription: %q,\n", sd.Description)
					b.WriteString("\t\t\t\tType: ")
					b.WriteString(renderToolTypeSpec(sd.Type))
					b.WriteString(",\n\t\t\t},\n")
				}
				b.WriteString("\t\t},\n")
			}
			if tool.ResultReminder != "" {
				fmt.Fprintf(&b, "\t\tResultReminder: %q,\n", tool.ResultReminder)
			}
			if tool.Confirmation != nil {
				b.WriteString("\t\tConfirmation: &tools.ConfirmationSpec{\n")
				fmt.Fprintf(&b, "\t\t\tTitle: %q,\n", tool.Confirmation.Title)
				fmt.Fprintf(&b, "\t\t\tPromptTemplate: %q,\n", tool.Confirmation.PromptTemplate)
				fmt.Fprintf(&b, "\t\t\tDeniedResultTemplate: %q,\n", tool.Confirmation.DeniedResultTemplate)
				b.WriteString("\t\t},\n")
			}
			b.WriteString("\t\tPayload: ")
			b.WriteString(renderToolTypeSpec(tool.Payload))
			b.WriteString(",\n")
			b.WriteString("\t\tResult: ")
			b.WriteString(renderToolResultSpec(tool.Result))
			b.WriteString(",\n")
			b.WriteString("\t}\n")
		}
		b.WriteString(")\n\nvar (\n\tmetadata = []policy.ToolMetadata{\n")
		for _, tool := range data.Tools {
			b.WriteString("\t\t{\n")
			fmt.Fprintf(&b, "\t\t\tID:          %s,\n", tool.ConstName)
			fmt.Fprintf(&b, "\t\t\tTitle:       %q,\n", tool.Title)
			fmt.Fprintf(&b, "\t\t\tDescription: %q,\n", tool.Description)
			b.WriteString("\t\t\tTags: []string{\n")
			for _, tag := range tool.Tags {
				fmt.Fprintf(&b, "\t\t\t\t%q,\n", tag)
			}
			b.WriteString("\t\t\t},\n")
			budgetClass := "policy.ToolBudgetClassBudgeted"
			if tool.Bookkeeping {
				budgetClass = "policy.ToolBudgetClassBookkeeping"
			}
			fmt.Fprintf(&b, "\t\t\tBudgetClass: %s,\n", budgetClass)
			b.WriteString("\t\t},\n")
		}
		b.WriteString("\t}\n\tnames = []tools.Ident{\n")
		for _, tool := range data.Tools {
			fmt.Fprintf(&b, "\t\t%s,\n", tool.ConstName)
		}
		b.WriteString("\t}\n)\n\n")
		b.WriteString("// Names returns the identifiers of all generated tools.\nfunc Names() []tools.Ident {\n\treturn names\n}\n\n")
		b.WriteString("// Spec returns the specification for the named tool if present.\nfunc Spec(name tools.Ident) (*tools.ToolSpec, bool) {\n\tswitch name {\n")
		for _, tool := range data.Tools {
			fmt.Fprintf(&b, "\tcase %s:\n\t\treturn &Spec%s, true\n", tool.ConstName, tool.ConstName)
		}
		b.WriteString("\tdefault:\n\t\treturn nil, false\n\t}\n}\n\n")
		b.WriteString("// PayloadSchema returns the JSON schema for the named tool payload.\nfunc PayloadSchema(name tools.Ident) ([]byte, bool) {\n\tswitch name {\n")
		for _, tool := range data.Tools {
			fmt.Fprintf(&b, "\tcase %s:\n\t\treturn Spec%s.Payload.Schema, true\n", tool.ConstName, tool.ConstName)
		}
		b.WriteString("\tdefault:\n\t\treturn nil, false\n\t}\n}\n\n")
		b.WriteString("// ResultSchema returns the JSON schema for the named tool result.\nfunc ResultSchema(name tools.Ident) ([]byte, bool) {\n\tswitch name {\n")
		for _, tool := range data.Tools {
			fmt.Fprintf(&b, "\tcase %s:\n\t\treturn Spec%s.Result.Schema, true\n", tool.ConstName, tool.ConstName)
		}
		b.WriteString("\tdefault:\n\t\treturn nil, false\n\t}\n}\n\n")
		b.WriteString("// Metadata exposes policy metadata for the generated tools.\nfunc Metadata() []policy.ToolMetadata {\n\treturn metadata\n}\n")
		return b.String()
	})
}

func renderToolTypeSpec(t *typeData) string {
	if t == nil {
		return "tools.TypeSpec{Name: \"\", Schema: nil, ExampleJSON: nil, ExampleInput: nil, Codec: tools.JSONCodec[any]{}}"
	}
	var b strings.Builder
	b.WriteString("tools.TypeSpec{")
	fmt.Fprintf(&b, "Name: %q, ", t.TypeName)
	if len(t.SchemaJSON) > 0 {
		fmt.Fprintf(&b, "Schema: []byte(%q), ", t.SchemaJSON)
	} else {
		b.WriteString("Schema: nil, ")
	}
	if len(t.ExampleJSON) > 0 {
		fmt.Fprintf(&b, "ExampleJSON: []byte(%q), ", t.ExampleJSON)
	} else {
		b.WriteString("ExampleJSON: nil, ")
	}
	if t.ExampleInputGo != "" {
		fmt.Fprintf(&b, "ExampleInput: %s, ", t.ExampleInputGo)
	} else {
		b.WriteString("ExampleInput: nil, ")
	}
	fmt.Fprintf(&b, "Codec: %s}", t.GenericCodec)
	return b.String()
}

func renderToolResultSpec(t *typeData) string {
	if t == nil {
		return "tools.TypeSpec{Name: \"\", Schema: nil, Codec: tools.JSONCodec[any]{}}"
	}
	var b strings.Builder
	b.WriteString("tools.TypeSpec{")
	fmt.Fprintf(&b, "Name: %q, ", t.TypeName)
	if len(t.SchemaJSON) > 0 {
		fmt.Fprintf(&b, "Schema: []byte(%q), ", t.SchemaJSON)
	} else {
		b.WriteString("Schema: nil, ")
	}
	fmt.Fprintf(&b, "Codec: %s}", t.GenericCodec)
	return b.String()
}

func writeIndentedBlock(b *strings.Builder, body string, indent int) {
	prefix := strings.Repeat("\t", indent)
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteString("\n")
	}
}
