package codegen

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/codegen/internal/jsontransport"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
)

type toolPayloadCodec struct {
	Name        string
	InputRef    string
	OutputRef   string
	Definitions map[string]string
	Unions      map[string]*jsontransport.UnionType
	Plan        codegen.TransformPlan
}

// buildToolPayloadCodec keeps JSON presence in a private transport graph until
// schema admission, then lets Loom materialize defaults in the service shape.
func (g *adapterGenerator) buildToolPayloadCodec(payload *expr.AttributeExpr, method string) (*toolPayloadCodec, error) {
	base := payload
	if ut, ok := base.Type.(expr.UserType); ok {
		base = ut.Attribute()
	}
	transport, locals, err := jsontransport.LocalizeNestedTypes(jsontransport.CloneWithJSONTags(base))
	if err != nil {
		return nil, err
	}
	scope := g.payloadScope
	input := &expr.AttributeExpr{Type: &expr.UserTypeExpr{TypeName: codegen.Goify(method, true) + "MCPInput", AttributeExpr: transport}}
	codec := &toolPayloadCodec{
		Name:        scope.Unique("decode" + codegen.Goify(method, true) + "Payload"),
		InputRef:    scope.GoTypeRef(input),
		OutputRef:   g.getTypeReference(payload),
		Definitions: make(map[string]string),
		Unions:      make(map[string]*jsontransport.UnionType),
	}
	codec.Definitions[scope.GoTypeName(input)] = scope.GoTypeDef(transport, true, false)
	for _, local := range locals {
		name := scope.GoTypeName(&expr.AttributeExpr{Type: local})
		codec.Definitions[name] = scope.GoTypeDef(local.Attribute(), true, false)
	}
	jsontransport.CollectUnions(input, scope, codec.Unions, make(map[string]struct{}), true)
	srcCtx := codegen.NewAttributeContext(true, false, false, "", scope)
	tgtCtx := codegen.NewAttributeContext(false, false, true, codegen.SnakeCase(g.originalService.Name), g.scope)
	codec.Plan, err = codegen.GoTransform(input, payload, "in", "out", srcCtx, tgtCtx, codec.Name, false)
	if err != nil {
		return nil, fmt.Errorf("build MCP payload conversion for %s: %w", method, err)
	}
	return codec, nil
}

func toolPayloadFile(genpkg string, svc *expr.ServiceExpr, data *AdapterData) *codegen.File {
	definitions := make(map[string]string)
	unions := make(map[string]*jsontransport.UnionType)
	var codecs []*ToolAdapter
	for _, tool := range data.Tools {
		if tool.PayloadCodec == nil {
			continue
		}
		codecs = append(codecs, tool)
		for name, definition := range tool.PayloadCodec.Definitions {
			definitions[name] = definition
		}
		for hash, union := range tool.PayloadCodec.Unions {
			unions[hash] = union
		}
	}
	if len(codecs) == 0 {
		return nil
	}
	unionList := make([]*jsontransport.UnionType, 0, len(unions))
	for _, union := range unions {
		unionList = append(unionList, union)
	}
	sort.Slice(unionList, func(i, j int) bool {
		return unionList[i].Name < unionList[j].Name
	})
	svcName := codegen.SnakeCase(svc.Name)
	return &codegen.File{
		Path: filepath.Join(codegen.Gendir, "mcp_"+svcName, "payloads.go"),
		Sections: []codegen.Section{
			codegen.Header("MCP tool payload decoding", data.MCPPackage, adapterImports(genpkg, svc, svcName, data)),
			payloadTypesSection(definitions),
			jsontransport.UnionsSection(unionList),
			payloadCodecsSection(codecs),
		},
	}
}

func payloadTypesSection(definitions map[string]string) codegen.Section {
	return codegen.NewRenderSection("mcp-payload-types", func() string {
		var b strings.Builder
		names := make([]string, 0, len(definitions))
		for name := range definitions {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "// %s preserves JSON field presence before service conversion.\ntype %s %s\n\n", name, name, definitions[name])
		}
		return b.String()
	})
}

func payloadCodecsSection(codecs []*ToolAdapter) codegen.Section {
	return codegen.NewRenderSection("mcp-payload-codecs", func() string {
		var b strings.Builder
		var helpers []*codegen.TransformFunctionData
		for _, tool := range codecs {
			renderPayloadCodec(&b, tool)
			helpers = codegen.AppendHelpers(helpers, tool.PayloadCodec.Plan.Helpers)
		}
		for _, helper := range helpers {
			fmt.Fprintf(&b, "func %s(v %s", helper.Name, helper.ParamTypeRef)
			if helper.ErrorAware {
				b.WriteString(", transformErr *error")
			}
			fmt.Fprintf(&b, ") %s {\n%s\nreturn res\n}\n\n", helper.ResultTypeRef, helper.Code)
		}
		return b.String()
	})
}

func renderPayloadCodec(b *strings.Builder, tool *ToolAdapter) {
	codec := tool.PayloadCodec
	fmt.Fprintf(b, "func %s(raw jsontext.Value) (out %s, err error) {\nvar in %s\n", codec.Name, codec.OutputRef, codec.InputRef)
	b.WriteString("if err = decodeMCPPayloadStrict(raw, &in); err != nil {\nreturn out, err\n}\n")
	renderPayloadAdmission(b, tool)
	fmt.Fprintf(b, "if err = sdkbridge.ValidateToolArguments(raw, %q); err != nil {\nreturn out, err\n}\n", tool.InputSchema)
	if codec.Plan.ErrorAware {
		b.WriteString("transformErr := &err\n")
	}
	b.WriteString(codec.Plan.Code)
	b.WriteString("\nreturn out, err\n}\n\n")
}

// renderPayloadAdmission retains the established top-level error messages before
// complete schema admission and conversion of the presence-preserving input.
func renderPayloadAdmission(b *strings.Builder, tool *ToolAdapter) {
	if len(tool.RequiredFields) == 0 && len(tool.EnumFields) == 0 {
		return
	}
	b.WriteString("fields, err := decodeMCPPayloadFields(raw)\nif err != nil {\nreturn out, err\n}\n")
	for _, field := range tool.RequiredFields {
		fmt.Fprintf(b, "if err = validateMCPPayloadRequired(fields, %q, %t); err != nil {\nreturn out, err\n}\n", field.Name, field.AllowsNull)
	}
	for _, field := range tool.EnumFields {
		fmt.Fprintf(b, "if err = validateMCPPayloadEnum(fields, %q, %t", field.Name, field.Pointer)
		for _, value := range field.Values {
			fmt.Fprintf(b, ", %q", value)
		}
		b.WriteString("); err != nil {\nreturn out, err\n}\n")
	}
}
