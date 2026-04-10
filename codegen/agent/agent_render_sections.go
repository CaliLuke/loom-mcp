package codegen

import (
	"fmt"
	"strings"

	gocodegen "github.com/CaliLuke/loom/codegen"
)

func toolTypesSection(data toolTypesFileData) gocodegen.Section {
	return gocodegen.MustRenderSection("tool-spec-types", func() string {
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
	return gocodegen.MustRenderSection("tool-transport-types", func() string {
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
	return gocodegen.MustRenderSection("tool-transport-validate", func() string {
		var b strings.Builder
		for _, t := range data.Types {
			if t == nil || len(t.TransportValidationSrc) == 0 {
				continue
			}
			fmt.Fprintf(&b, "// Validate%s runs the validations defined on %s.\n", t.TransportTypeName, t.TransportTypeName)
			fmt.Fprintf(&b, "func Validate%s(body %s) (err error) {\n", t.TransportTypeName, t.TransportTypeRef)
			for _, line := range t.TransportValidationSrc {
				if strings.TrimSpace(line) == "" {
					b.WriteString("\n")
					continue
				}
				b.WriteString("\t")
				b.WriteString(line)
				b.WriteString("\n")
			}
			b.WriteString("\treturn\n")
			b.WriteString("}\n\n")
		}
		return b.String()
	})
}

func toolTransformsSection(data transformsFileData) gocodegen.Section {
	return gocodegen.MustRenderSection("tool-transforms", func() string {
		var b strings.Builder
		for _, fn := range data.Functions {
			fmt.Fprintf(&b, "// %s converts %s to %s.\n", fn.Name, fn.ParamTypeRef, fn.ResultTypeRef)
			fmt.Fprintf(&b, "func %s(in %s) %s {\n", fn.Name, fn.ParamTypeRef, fn.ResultTypeRef)
			if fn.NilInputReturnsNil {
				b.WriteString("\tif in == nil {\n")
				b.WriteString("\t\treturn nil\n")
				b.WriteString("\t}\n")
			}
			fmt.Fprintf(&b, "\tvar out %s\n", fn.ResultTypeRef)
			writeIndentedBlock(&b, fn.Body, 1)
			b.WriteString("\treturn out\n")
			b.WriteString("}\n\n")
		}
		b.WriteString("// Helper transform functions\n")
		for _, helper := range data.Helpers {
			if helper == nil {
				continue
			}
			fmt.Fprintf(&b, "func %s(v %s) %s {\n", helper.Name, helper.ParamTypeRef, helper.ResultTypeRef)
			writeIndentedBlock(&b, helper.Code, 1)
			b.WriteString("\treturn res\n")
			b.WriteString("}\n\n")
		}
		return b.String()
	})
}

func toolUnionTypesSection(data toolUnionTypesFileData) gocodegen.Section {
	return gocodegen.MustRenderSection("tool-spec-union-types", func() string {
		var b strings.Builder
		for i, u := range data.Unions {
			if u == nil {
				continue
			}
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "// %s is a sum-type union.\n", u.Name)
			fmt.Fprintf(&b, "type %s struct {\n", u.Name)
			fmt.Fprintf(&b, "\tkind %s\n", u.KindName)
			for _, f := range u.Fields {
				fmt.Fprintf(&b, "\t%s %s\n", f.FieldName, f.FieldType)
			}
			b.WriteString("}\n\n")
			fmt.Fprintf(&b, "// %s enumerates the union variants for %s.\n", u.KindName, u.Name)
			fmt.Fprintf(&b, "type %s string\n\n", u.KindName)
			b.WriteString("const (\n")
			for _, f := range u.Fields {
				fmt.Fprintf(&b, "\t// %s identifies the %s branch of the union.\n", f.KindConst, f.Name)
				fmt.Fprintf(&b, "\t%s %s = %q\n", f.KindConst, u.KindName, f.TypeTag)
			}
			b.WriteString(")\n\n")
			fmt.Fprintf(&b, "// Kind returns the discriminator value of the union.\nfunc (u %s) Kind() %s {\n\treturn u.kind\n}\n\n", u.Name, u.KindName)
			for _, f := range u.Fields {
				fmt.Fprintf(&b, "// New%s%s constructs a %s with the %s branch set.\n", u.Name, f.FieldName, u.Name, f.Name)
				fmt.Fprintf(&b, "func New%s%s(v %s) %s {\n", u.Name, f.FieldName, f.FieldType, u.Name)
				fmt.Fprintf(&b, "\treturn %s{\n\t\tkind: %s,\n\t\t%s: v,\n\t}\n}\n\n", u.Name, f.KindConst, f.FieldName)
				fmt.Fprintf(&b, "// As%s returns the value of the %s branch if set.\n", f.FieldName, f.Name)
				fmt.Fprintf(&b, "func (u %s) As%s() (_ %s, ok bool) {\n", u.Name, f.FieldName, f.FieldType)
				fmt.Fprintf(&b, "\tif u.kind != %s {\n\t\treturn\n\t}\n\treturn u.%s, true\n}\n\n", f.KindConst, f.FieldName)
				fmt.Fprintf(&b, "// Set%s sets the %s branch of the union.\n", f.FieldName, f.Name)
				fmt.Fprintf(&b, "func (u *%s) Set%s(v %s) {\n\tu.kind = %s\n\tu.%s = v\n}\n\n", u.Name, f.FieldName, f.FieldType, f.KindConst, f.FieldName)
			}
			fmt.Fprintf(&b, "// Validate ensures the union discriminant is valid.\nfunc (u %s) Validate() error {\n\tswitch u.kind {\n\tcase \"\":\n\t\treturn goa.InvalidEnumValueError(\"type\", \"\", []any{\n", u.Name)
			for _, f := range u.Fields {
				fmt.Fprintf(&b, "\t\t\tstring(%s),\n", f.KindConst)
			}
			b.WriteString("\t\t})\n")
			for _, f := range u.Fields {
				fmt.Fprintf(&b, "\tcase %s:\n\t\treturn nil\n", f.KindConst)
			}
			b.WriteString("\tdefault:\n\t\treturn goa.InvalidEnumValueError(\"type\", u.kind, []any{\n")
			for _, f := range u.Fields {
				fmt.Fprintf(&b, "\t\t\tstring(%s),\n", f.KindConst)
			}
			b.WriteString("\t\t})\n\t}\n}\n\n")
			fmt.Fprintf(&b, "// MarshalJSON marshals the union into the canonical {type,value} JSON shape.\nfunc (u %s) MarshalJSON() ([]byte, error) {\n", u.Name)
			b.WriteString("\tif err := u.Validate(); err != nil {\n\t\treturn nil, err\n\t}\n\tvar (\n\t\tvalue any\n\t)\n\tswitch u.kind {\n")
			for _, f := range u.Fields {
				fmt.Fprintf(&b, "\tcase %s:\n\t\tvalue = u.%s\n", f.KindConst, f.FieldName)
			}
			fmt.Fprintf(&b, "\tdefault:\n\t\treturn nil, fmt.Errorf(\"unexpected %s discriminant %%q\", u.kind)\n\t}\n", u.Name)
			b.WriteString("\treturn json.Marshal(struct {\n\t\tType string `json:\"type\"`\n\t\tValue any `json:\"value\"`\n\t}{\n\t\tType: string(u.kind),\n\t\tValue: value,\n\t})\n}\n\n")
			fmt.Fprintf(&b, "// UnmarshalJSON unmarshals the union from the canonical {type,value} JSON shape.\nfunc (u *%s) UnmarshalJSON(data []byte) error {\n", u.Name)
			b.WriteString("\tvar raw struct {\n\t\tType string `json:\"type\"`\n\t\tValue json.RawMessage `json:\"value\"`\n\t}\n")
			b.WriteString("\tif err := json.Unmarshal(data, &raw); err != nil {\n\t\treturn err\n\t}\n\tswitch raw.Type {\n")
			for _, f := range u.Fields {
				fmt.Fprintf(&b, "\tcase string(%s):\n\t\tvar v %s\n\t\tif err := json.Unmarshal(raw.Value, &v); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tu.kind = %s\n\t\tu.%s = v\n", f.KindConst, f.FieldType, f.KindConst, f.FieldName)
			}
			fmt.Fprintf(&b, "\tdefault:\n\t\treturn fmt.Errorf(\"unexpected %s type %%q\", raw.Type)\n\t}\n\treturn nil\n}\n", u.Name)
		}
		return b.String()
	})
}

func toolSpecsSection(data toolSpecFileData) gocodegen.Section {
	return gocodegen.MustRenderSection("tool-specs", func() string {
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
			b.WriteString("\t\t\t},\n\t\t},\n")
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
