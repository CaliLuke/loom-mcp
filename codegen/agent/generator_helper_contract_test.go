package codegen

import (
	"errors"
	"testing"

	"github.com/CaliLuke/loom/codegen/service"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentsExpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
)

func TestTemplateFuncMapClassifiesProviderAndTypeShapes(t *testing.T) {
	funcs := templateFuncMap()
	isMetaInject := funcs["isMetaInject"].(func(string) bool)
	for _, field := range []string{"run_id", "session_id", "turn_id", "tool_call_id", "parent_tool_call_id"} {
		assert.True(t, isMetaInject(field), field)
	}
	assert.False(t, isMetaInject("query"))

	assert.True(t, funcs["isAPIKey"].(func(goaexpr.SchemeKind) bool)(goaexpr.APIKeyKind))
	assert.False(t, funcs["isAPIKey"].(func(goaexpr.SchemeKind) bool)(goaexpr.OAuth2Kind))
	assert.True(t, funcs["isOAuth2"].(func(goaexpr.SchemeKind) bool)(goaexpr.OAuth2Kind))
	assert.True(t, funcs["isJWT"].(func(goaexpr.SchemeKind) bool)(goaexpr.JWTKind))
	assert.True(t, funcs["isBasicAuth"].(func(goaexpr.SchemeKind) bool)(goaexpr.BasicAuthKind))

	hasMethodBackedTools := funcs["hasMethodBackedTools"].(func(*ToolsetData) bool)
	assert.False(t, hasMethodBackedTools(nil))
	assert.False(t, hasMethodBackedTools(&ToolsetData{}))
	assert.False(t, hasMethodBackedTools(&ToolsetData{Tools: []*ToolData{nil, {}}}))
	methodToolset := &ToolsetData{Tools: []*ToolData{{IsMethodBacked: true}}}
	assert.True(t, hasMethodBackedTools(methodToolset))

	hasExportedTools := funcs["hasExportedTools"].(func(*ToolsetData) bool)
	assert.False(t, hasExportedTools(nil))
	assert.False(t, hasExportedTools(&ToolsetData{Tools: []*ToolData{nil, {}}}))
	assert.True(t, hasExportedTools(&ToolsetData{Tools: []*ToolData{{IsExportedByAgent: true}}}))

	hasServiceSideProviders := funcs["hasServiceSideProviders"].(func(*GeneratorData) bool)
	assert.False(t, hasServiceSideProviders(nil))
	assert.False(t, hasServiceSideProviders(&GeneratorData{Services: []*ServiceAgentsData{nil, {}}}))
	serviceToolset := &ToolsetData{
		SpecsDir:      "gen/service/toolsets/search",
		SourceService: &service.Data{Name: "service"},
		Tools:         []*ToolData{{IsMethodBacked: true}},
	}
	data := &GeneratorData{Services: []*ServiceAgentsData{{Agents: []*AgentData{{AllToolsets: []*ToolsetData{serviceToolset}}}}}}
	assert.True(t, hasServiceSideProviders(data))
	serviceToolset.IsRegistryBacked = true
	assert.False(t, hasServiceSideProviders(data))

	skillPreloadRef := funcs["skillPreloadRef"].(func(agentsExpr.SkillPreloadMode) string)
	assert.Equal(t, "agentsruntime.SkillPreloadOnStart", skillPreloadRef(agentsExpr.SkillPreloadOnStart))
	assert.Equal(t, "agentsruntime.SkillPreloadNone", skillPreloadRef(agentsExpr.SkillPreloadNone))
	assert.Equal(t, "agentsruntime.SkillPreloadNone", skillPreloadRef("unknown"))
	skillReloadRef := funcs["skillReloadRef"].(func(agentsExpr.SkillReloadMode) string)
	assert.Equal(t, "agentsruntime.SkillReloadPerCall", skillReloadRef(agentsExpr.SkillReloadPerCall))
	assert.Equal(t, "agentsruntime.SkillReloadNever", skillReloadRef(agentsExpr.SkillReloadNever))
	assert.Equal(t, "agentsruntime.SkillReloadNever", skillReloadRef("unknown"))

	memoryToolsetUsesLongTerm := funcs["memoryToolsetUsesLongTerm"].(func(*ToolsetData) bool)
	assert.False(t, memoryToolsetUsesLongTerm(nil))
	assert.False(t, memoryToolsetUsesLongTerm(&ToolsetData{Expr: &agentsExpr.ToolsetExpr{}}))
	memoryToolset := &ToolsetData{Expr: &agentsExpr.ToolsetExpr{Provider: &agentsExpr.ProviderExpr{
		MemorySources: []agentsExpr.MemoryToolSource{agentsExpr.MemoryToolSourceTranscript, agentsExpr.MemoryToolSourceLongTerm},
	}}}
	assert.True(t, memoryToolsetUsesLongTerm(memoryToolset))
	memoryToolset.Expr.Provider.MemorySources = []agentsExpr.MemoryToolSource{agentsExpr.MemoryToolSourceTranscript}
	assert.False(t, memoryToolsetUsesLongTerm(memoryToolset))

	memoryToolSourceRef := funcs["memoryToolSourceRef"].(func(agentsExpr.MemoryToolSource) string)
	assert.Equal(t, "memory.ToolSourceTranscript", memoryToolSourceRef(agentsExpr.MemoryToolSourceTranscript))
	assert.Equal(t, "memory.ToolSourceIndexedTranscript", memoryToolSourceRef(agentsExpr.MemoryToolSourceIndexedTranscript))
	assert.Equal(t, "memory.ToolSourceLongTerm", memoryToolSourceRef(agentsExpr.MemoryToolSourceLongTerm))
	assert.Equal(t, "memory.ToolSourceTranscript", memoryToolSourceRef("unknown"))
	memoryVisibilityRef := funcs["memoryVisibilityRef"].(func(agentsExpr.MemoryVisibility) string)
	assert.Equal(t, "memory.VisibilityShared", memoryVisibilityRef(agentsExpr.MemoryVisibilityShared))
	assert.Equal(t, "memory.VisibilityUser", memoryVisibilityRef(agentsExpr.MemoryVisibilityUser))
	assert.Equal(t, "memory.VisibilityUser", memoryVisibilityRef("unknown"))

	isRegistryBacked := funcs["isRegistryBacked"].(func(*ToolsetData) bool)
	assert.False(t, isRegistryBacked(nil))
	assert.True(t, isRegistryBacked(&ToolsetData{IsRegistryBacked: true}))
	mcpService := funcs["mcpService"].(func(*ToolsetData) string)
	assert.Empty(t, mcpService(nil))
	assert.Empty(t, mcpService(&ToolsetData{Expr: &agentsExpr.ToolsetExpr{}}))
	assert.Equal(t, "catalog", mcpService(&ToolsetData{Expr: &agentsExpr.ToolsetExpr{Provider: &agentsExpr.ProviderExpr{MCPService: "catalog"}}}))

	simpleField := funcs["simpleField"].(func(*goaexpr.AttributeExpr, string) bool)
	fieldsOf := funcs["fieldsOf"].(func(*goaexpr.AttributeExpr) []string)
	shape := objectAttribute(
		field("zeta", goaexpr.String),
		field("alpha", &goaexpr.Array{ElemType: &goaexpr.AttributeExpr{Type: goaexpr.Int}}),
		field("nested", &goaexpr.Object{field("value", goaexpr.String)}),
	)
	assert.True(t, simpleField(shape, "zeta"))
	assert.True(t, simpleField(shape, "alpha"))
	assert.False(t, simpleField(shape, "nested"))
	assert.False(t, simpleField(shape, "missing"))
	assert.False(t, simpleField(nil, "zeta"))
	assert.Equal(t, []string{"alpha", "nested", "zeta"}, fieldsOf(shape))
	assert.Nil(t, fieldsOf(nil))
	assert.Nil(t, fieldsOf(&goaexpr.AttributeExpr{Type: goaexpr.String}))

	userType := &goaexpr.UserTypeExpr{TypeName: "Shape", AttributeExpr: shape}
	assert.Equal(t, []string{"alpha", "nested", "zeta"}, fieldsOf(&goaexpr.AttributeExpr{Type: userType}))
}

func TestTransformHelpersPreserveTypeAndNilabilityContracts(t *testing.T) {
	used := map[string]struct{}{"pkg": {}, "pkg2": {}}
	assert.Equal(t, "pkg3", uniqueImportAlias(used, "pkg"))
	assert.Equal(t, "other", uniqueImportAlias(used, "other"))
	assert.Equal(t, "pkg4", uniqueImportAlias(used, ""))

	tool := &ToolData{QualifiedName: "catalog.search", Toolset: &ToolsetData{QualifiedName: "catalog"}}
	source := &goaexpr.AttributeExpr{Type: goaexpr.String}
	target := &goaexpr.AttributeExpr{Type: goaexpr.Int}
	wantErr := errors.New("incompatible")
	compatErr := transformCompatibilityError(tool, "payload", source, target, wantErr)
	require.ErrorIs(t, compatErr, wantErr)
	require.ErrorContains(t, compatErr, `method-backed tool "catalog.search" in toolset "catalog" has incompatible payload transform from string to int`)
	buildErr := transformBuildError(tool, "result", source, target, wantErr)
	require.ErrorIs(t, buildErr, wantErr)
	require.ErrorContains(t, buildErr, `failed to build result transform`)
	assert.Equal(t, "<unknown>", toolsetQualifiedName(nil))
	assert.Equal(t, "<nil>", attrTypeName(nil))
	assert.Equal(t, "string", attrTypeName(source))

	public := &goaexpr.AttributeExpr{Type: goaexpr.Boolean}
	specs := &toolSpecsData{order: []*typeData{nil, {TypeName: "Other"}, {TypeName: "Wanted", PublicType: public}}}
	assert.Same(t, public, findToolTypeAttribute(specs, "Wanted"))
	assert.Nil(t, findToolTypeAttribute(specs, "Missing"))
	assert.Equal(t, "defaultpkg", typeRefDefaultPackage("defaultpkg", nil))
	assert.Equal(t, "defaultpkg", typeRefDefaultPackage("defaultpkg", &goaexpr.AttributeExpr{Type: goaexpr.Empty}))

	optionalString := field("label", goaexpr.String)
	optionalObject := field("details", &goaexpr.Object{field("value", goaexpr.String)})
	result := objectAttribute(optionalString, optionalObject)
	assert.True(t, serverDataSourceMayBeNil(result, "label", optionalString.Attribute))
	assert.True(t, serverDataSourceMayBeNil(result, "details", optionalObject.Attribute))
	result.Validation = &goaexpr.ValidationExpr{Required: []string{"label", "details"}}
	assert.False(t, serverDataSourceMayBeNil(result, "label", optionalString.Attribute))
	assert.False(t, serverDataSourceMayBeNil(result, "details", optionalObject.Attribute))
	assert.False(t, serverDataSourceMayBeNil(nil, "label", optionalString.Attribute))
	assert.False(t, serverDataSourceMayBeNil(result, "", optionalString.Attribute))
	assert.False(t, serverDataSourceMayBeNil(result, "label", nil))
}

func TestSimpleAttributeClassification(t *testing.T) {
	assert.False(t, isSimpleAttr(nil))
	assert.False(t, isSimpleAttr(&goaexpr.AttributeExpr{}))
	assert.True(t, isSimpleAttr(&goaexpr.AttributeExpr{Type: goaexpr.String}))
	assert.True(t, isSimpleAttr(&goaexpr.AttributeExpr{Type: &goaexpr.Array{ElemType: &goaexpr.AttributeExpr{Type: goaexpr.Int}}}))
	assert.True(t, isSimpleAttr(&goaexpr.AttributeExpr{Type: &goaexpr.Map{
		KeyType:  &goaexpr.AttributeExpr{Type: goaexpr.String},
		ElemType: &goaexpr.AttributeExpr{Type: goaexpr.Boolean},
	}}))
	assert.False(t, isSimpleAttr(objectAttribute(field("value", goaexpr.String))))
	userType := &goaexpr.UserTypeExpr{TypeName: "Label", AttributeExpr: &goaexpr.AttributeExpr{Type: goaexpr.String}}
	assert.False(t, isSimpleAttr(&goaexpr.AttributeExpr{Type: userType}))
	assert.Same(t, userType.AttributeExpr, resolve(&goaexpr.AttributeExpr{Type: userType}))
	assert.Nil(t, resolve(nil))
}

func objectAttribute(fields ...*goaexpr.NamedAttributeExpr) *goaexpr.AttributeExpr {
	object := goaexpr.Object(fields)
	return &goaexpr.AttributeExpr{Type: &object}
}

func field(name string, dataType goaexpr.DataType) *goaexpr.NamedAttributeExpr {
	return &goaexpr.NamedAttributeExpr{Name: name, Attribute: &goaexpr.AttributeExpr{Type: dataType}}
}
