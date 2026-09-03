package codegen

import (
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/codegen/naming"
	"github.com/CaliLuke/loom-mcp/v2/codegen/shared"
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
)

type (
	// IconData represents one icon metadata entry in generated templates.
	IconData struct {
		Source   string
		MIMEType string
		Sizes    []string
		Theme    string
	}

	// AnnotationMetaEntry stores one generated tool annotation entry.
	AnnotationMetaEntry struct {
		Key    string
		Values []string
	}

	// AdapterData holds the data for generating the adapter
	AdapterData struct {
		ServiceName           string
		ServiceGoName         string
		MCPServiceName        string
		MCPName               string
		MCPVersion            string
		MCPDescription        string
		WebsiteURL            string
		Icons                 []*IconData
		Package               string
		MCPPackage            string
		ImportPath            string
		Tools                 []*ToolAdapter
		Resources             []*ResourceAdapter
		SkillDirectories      []*SkillDirectoryAdapter
		StaticPrompts         []*StaticPromptAdapter
		DynamicPrompts        []*DynamicPromptAdapter
		HasWatchableResources bool

		Register   *RegisterData
		OAuth      *OAuthData
		ToolSearch *ToolSearchData
	}

	// OAuthData carries the OAuth protected-resource configuration into
	// generation. Nil when the DSL does not declare OAuth.
	OAuthData struct {
		AuthorizationServers     []string
		Scopes                   []OAuthScopeData
		ResourceIdentifier       string
		BearerMethodsSupported   []string
		ResourceDocumentationURL string
		// TrustProxyHeaders controls whether generated code honors
		// X-Forwarded-* and RFC 7239 Forwarded headers when deriving
		// the canonical resource URL and the WWW-Authenticate
		// challenge origin. Default is false: a server reachable
		// directly by clients must not trust forwarded headers because
		// an attacker could otherwise control the PRM `resource` field.
		TrustProxyHeaders bool
	}

	// ToolSearchData carries generated progressive discovery ranking defaults.
	ToolSearchData struct {
		DefaultMaxResults int
		MinScore          int
		ExactMatchMode    string
		FuzzyNameMatching bool
		BroadFallback     bool
		NameWeight        int
		TitleWeight       int
		MetadataWeight    int
		DescriptionWeight int
		ParameterWeight   int
		FuzzyNameWeight   int
	}

	// OAuthScopeData describes one scope advertised by the protected
	// resource.
	OAuthScopeData struct {
		Name        string
		Description string
	}

	// RegisterData drives generation of runtime registration helpers.
	RegisterData struct {
		Package            string
		HelperName         string
		ServiceName        string
		SuiteName          string
		SuiteQualifiedName string
		Description        string
		Tools              []RegisterTool
	}

	// RegisterTool represents a single tool entry in the helper file.
	RegisterTool struct {
		ID            string
		QualifiedName string
		Description   string
		Meta          []AnnotationMetaEntry
		PayloadType   string
		ResultType    string
		InputSchema   string
		ExampleArgs   string
	}

	// ToolAdapter represents a tool adapter
	ToolAdapter struct {
		Name               string
		Description        string
		Title              string
		DiscoveryCategory  string
		DiscoveryTags      []string
		DiscoveryKeywords  []string
		Icons              []*IconData
		OriginalMethodName string
		Meta               []AnnotationMetaEntry
		MetaJSON           string
		AnnotationsJSON    string
		HasPayload         bool
		HasResult          bool
		PayloadType        string
		ResultType         string
		InputSchema        string
		OutputSchema       string
		IsStreaming        bool
		StreamInterface    string
		StreamEventType    string
		// Simple validations (top-level only)
		RequiredFields []string
		EnumFields     []EnumField
		DefaultFields  []DefaultField
		// ExampleArguments contains a minimal valid JSON for tool arguments
		ExampleArguments string
		// CanonicalExampleJSON is a deterministic, always-valid JSON example of
		// the payload, synthesized from the IR. Used by the per-tool input
		// recovery function emitted into the adapter.
		CanonicalExampleJSON string
		// UnionEnvelopes lists, for each top-level payload field whose type is
		// a discriminated union, the metadata the recovery hint needs to
		// describe valid discriminator values and the configured branch key.
		UnionEnvelopes []UnionEnvelopeMeta
		// Projected is non-nil when this adapter comes from an agent toolset
		// projected into MCP instead of a method-level MCP Tool declaration.
		Projected *ProjectedToolAdapter
	}

	// ProjectedToolAdapter carries ownership data for method-backed toolset
	// tools projected into MCP.
	ProjectedToolAdapter struct {
		SourceToolset       string
		SourceTool          string
		Description         string
		Title               string
		PlacementService    string
		PlacementMCPServer  string
		SpecsPackageName    string
		SpecsImportPath     string
		SpecName            string
		BoundService        string
		BoundMethod         string
		MethodPayloadType   string
		RuntimeToolName     string
		DispatcherFuncName  string
		DispatchOptionsName string
		QualifiedSourceTool string
		HasPayload          bool
		HasResult           bool
		InjectedFields      []string
		HasBounds           bool
		InputSchema         string
		OutputSchema        string
		ExampleArguments    string
	}

	// DefaultField describes a top-level payload field default assignment.
	DefaultField struct {
		Name    string
		GoName  string
		Literal string
		Kind    string
	}

	// EnumField describes a top-level payload enum validation in declaration order.
	EnumField struct {
		Name    string
		Values  []string
		Pointer bool
	}

	// ResourceAdapter represents a resource adapter
	ResourceAdapter struct {
		Name               string
		Description        string
		URI                string
		MimeType           string
		Icons              []*IconData
		OriginalMethodName string
		HasPayload         bool
		HasResult          bool
		PayloadType        string
		ResultType         string
		QueryFields        []*ResourceQueryField
		Watchable          bool
		IsStreaming        bool
		StreamInterface    string
		StreamEventType    string
	}

	// ResourceQueryField describes one statically known query parameter binding
	// for a resource payload field.
	ResourceQueryField struct {
		QueryKey       string
		GuardExpr      string
		ValueExpr      string
		CollectionExpr string
		FormatKind     string
		Repeated       bool
	}

	// SkillDirectoryAdapter represents one skill root exposed as MCP resources.
	SkillDirectoryAdapter struct {
		Root string
	}

	// resourceQueryFieldDefinition captures one flattened top-level resource
	// query field together with the presence rules implied by the Goa payload.
	resourceQueryFieldDefinition struct {
		Attribute        *expr.AttributeExpr
		Required         bool
		PrimitivePointer bool
	}

	// StaticPromptAdapter represents a static prompt
	StaticPromptAdapter struct {
		Name          string
		Description   string
		Icons         []*IconData
		Messages      []*PromptMessageAdapter
		RuntimePrompt *RuntimePromptAdapter
	}

	// RuntimePromptAdapter represents a runtime prompt spec generated from a
	// static MCP prompt.
	RuntimePromptAdapter struct {
		AgentID  string
		Role     string
		Template string
		Version  string
	}

	// PromptMessageAdapter represents a prompt message
	PromptMessageAdapter struct {
		Role    string
		Content string
	}

	// DynamicPromptAdapter represents a dynamic prompt adapter
	DynamicPromptAdapter struct {
		Name               string
		Description        string
		Icons              []*IconData
		OriginalMethodName string
		HasPayload         bool
		PayloadType        string
		ResultType         string
		// Arguments describes prompt arguments derived from the payload (dynamic prompts)
		Arguments []PromptArg
		// ExampleArguments contains a minimal valid JSON for prompt arguments
		ExampleArguments string
	}

	// PromptArg is a lightweight representation for generating PromptArgument values
	PromptArg struct {
		Name        string
		Description string
		Required    bool
		Values      []string
	}

	// adapterGenerator generates the adapter layer between MCP and the original service
	adapterGenerator struct {
		genpkg          string
		originalService *expr.ServiceExpr
		mcp             *mcpexpr.MCPExpr
		mapping         *ServiceMethodMapping
		projected       []*ProjectedToolAdapter
		scope           *codegen.NameScope
	}
)

const (
	resourceQueryFormatString  = "string"
	resourceQueryFormatBool    = "bool"
	resourceQueryFormatInt     = "int"
	resourceQueryFormatUint    = "uint"
	resourceQueryFormatFloat32 = "float32"
	resourceQueryFormatFloat64 = "float64"

	defaultToolSearchMaxResults        = 10
	defaultToolSearchNameWeight        = 1000
	defaultToolSearchTitleWeight       = 900
	defaultToolSearchMetadataWeight    = 400
	defaultToolSearchDescriptionWeight = 250
	defaultToolSearchParameterWeight   = 100
	defaultToolSearchFuzzyNameWeight   = 600
)

// newAdapterGenerator creates a new adapter generator
func newAdapterGenerator(
	genpkg string,
	svc *expr.ServiceExpr,
	mcp *mcpexpr.MCPExpr,
	mapping *ServiceMethodMapping,
	projected []*ProjectedToolAdapter,
) *adapterGenerator {
	return &adapterGenerator{
		genpkg:          genpkg,
		originalService: svc,
		mcp:             mcp,
		mapping:         mapping,
		projected:       projected,
		scope:           codegen.NewNameScope(),
	}
}

// Private methods

// buildAdapterData creates the data for the adapter template.
func (g *adapterGenerator) buildAdapterData() (*AdapterData, error) {
	tools, err := g.buildToolAdapters()
	if err != nil {
		return nil, err
	}
	resources, err := g.buildResourceAdapters()
	if err != nil {
		return nil, err
	}
	data := g.newAdapterData(tools, resources)
	g.populateAdapterDataCollections(data)
	g.populateAdapterDataFlags(data)
	g.populateAdapterHelperData(data)
	return data, nil
}

func (g *adapterGenerator) newAdapterData(tools []*ToolAdapter, resources []*ResourceAdapter) *AdapterData {
	return &AdapterData{
		ServiceName:      g.originalService.Name,
		ServiceGoName:    codegen.Goify(g.originalService.Name, true),
		MCPServiceName:   g.originalService.Name,
		MCPName:          g.mcp.Name,
		MCPVersion:       g.mcp.Version,
		MCPDescription:   g.mcp.Description,
		WebsiteURL:       g.mcp.WebsiteURL,
		OAuth:            oauthDataFromExpr(g.mcp.OAuth),
		ToolSearch:       toolSearchDataFromExpr(g.mcp.ToolSearch),
		Icons:            iconDataFromExprs(g.mcp.Icons),
		Package:          codegen.SnakeCase(g.originalService.Name),
		MCPPackage:       "mcp" + strings.ToLower(codegen.Goify(g.originalService.Name, false)),
		ImportPath:       g.genpkg,
		Tools:            tools,
		Resources:        resources,
		SkillDirectories: g.buildSkillDirectoryAdapters(),
	}
}

func (g *adapterGenerator) populateAdapterDataCollections(data *AdapterData) {
	data.DynamicPrompts = g.buildDynamicPromptAdapters()
	data.StaticPrompts = g.buildStaticPrompts()
}

func (g *adapterGenerator) populateAdapterDataFlags(data *AdapterData) {
	data.HasWatchableResources = hasWatchableResources(data.Resources)
}

func (g *adapterGenerator) populateAdapterHelperData(data *AdapterData) {
	data.Register = g.buildRegisterData(data)
}

func toolSearchDataFromExpr(search *mcpexpr.ToolSearchExpr) *ToolSearchData {
	data := &ToolSearchData{
		DefaultMaxResults: defaultToolSearchMaxResults,
		ExactMatchMode:    mcpexpr.ToolSearchExactMatchNarrow,
		FuzzyNameMatching: true,
		BroadFallback:     true,
		NameWeight:        defaultToolSearchNameWeight,
		TitleWeight:       defaultToolSearchTitleWeight,
		MetadataWeight:    defaultToolSearchMetadataWeight,
		DescriptionWeight: defaultToolSearchDescriptionWeight,
		ParameterWeight:   defaultToolSearchParameterWeight,
		FuzzyNameWeight:   defaultToolSearchFuzzyNameWeight,
	}
	if search == nil {
		return data
	}
	if search.DefaultMaxResults > 0 {
		data.DefaultMaxResults = search.DefaultMaxResults
	}
	data.MinScore = search.MinScore
	if search.ExactMatchMode != "" {
		data.ExactMatchMode = search.ExactMatchMode
	}
	if search.FuzzyNameMatching != nil {
		data.FuzzyNameMatching = *search.FuzzyNameMatching
	}
	if search.BroadFallback != nil {
		data.BroadFallback = *search.BroadFallback
	}
	data.NameWeight = toolSearchWeightValue(search.Weights.Name, data.NameWeight)
	data.TitleWeight = toolSearchWeightValue(search.Weights.Title, data.TitleWeight)
	data.MetadataWeight = toolSearchWeightValue(search.Weights.Metadata, data.MetadataWeight)
	data.DescriptionWeight = toolSearchWeightValue(search.Weights.Description, data.DescriptionWeight)
	data.ParameterWeight = toolSearchWeightValue(search.Weights.Parameters, data.ParameterWeight)
	data.FuzzyNameWeight = toolSearchWeightValue(search.Weights.FuzzyName, data.FuzzyNameWeight)
	return data
}

func toolSearchWeightValue(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func (g *adapterGenerator) buildRegisterData(data *AdapterData) *RegisterData {
	if len(data.Tools) == 0 {
		return nil
	}
	serviceGoName := data.ServiceGoName
	suiteGoName := codegen.Goify(g.mcp.Name, true)
	desc := g.mcp.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP toolset %s.%s", g.originalService.Name, g.mcp.Name)
	}
	helper := serviceGoName + suiteGoName + "Toolset"
	reg := &RegisterData{
		Package:            data.MCPPackage,
		HelperName:         helper,
		ServiceName:        g.originalService.Name,
		SuiteName:          g.mcp.Name,
		SuiteQualifiedName: fmt.Sprintf("%s.%s", g.originalService.Name, g.mcp.Name),
		Description:        desc,
	}
	for _, tool := range data.Tools {
		schema := tool.InputSchema
		if schema == "" {
			schema = "{}"
		}
		payloadType := tool.PayloadType
		if payloadType == "" {
			payloadType = "any"
		}
		resultType := tool.ResultType
		if resultType == "" {
			resultType = "any"
		}
		reg.Tools = append(reg.Tools, RegisterTool{
			ID:            tool.Name,
			QualifiedName: fmt.Sprintf("%s.%s.%s", reg.ServiceName, reg.SuiteName, tool.Name),
			Description:   tool.Description,
			Meta:          tool.Meta,
			PayloadType:   payloadType,
			ResultType:    resultType,
			InputSchema:   schema,
			ExampleArgs:   tool.ExampleArguments,
		})
	}
	return reg
}

func adapterDataHasResources(data *AdapterData) bool {
	return len(data.Resources) > 0 || len(data.SkillDirectories) > 0
}

func hasWatchableResources(resources []*ResourceAdapter) bool {
	for _, resource := range resources {
		if resource.Watchable {
			return true
		}
	}
	return false
}

// buildToolAdapters creates adapter data for tools.
func (g *adapterGenerator) buildToolAdapters() ([]*ToolAdapter, error) {
	adapters := make([]*ToolAdapter, 0, len(g.mcp.Tools)+len(g.projected))
	seen := make(map[string]struct{}, len(g.mcp.Tools)+len(g.projected))

	for _, tool := range g.mcp.Tools {
		adapter, err := g.buildToolAdapter(tool)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[adapter.Name]; ok {
			return nil, fmt.Errorf("duplicate MCP tool %q", adapter.Name)
		}
		seen[adapter.Name] = struct{}{}
		adapters = append(adapters, adapter)
	}
	for _, projected := range g.projected {
		adapter := g.buildProjectedToolAdapter(projected)
		if _, ok := seen[adapter.Name]; ok {
			return nil, fmt.Errorf("duplicate MCP tool %q", adapter.Name)
		}
		seen[adapter.Name] = struct{}{}
		adapters = append(adapters, adapter)
	}

	return adapters, nil
}

func (g *adapterGenerator) buildProjectedToolAdapter(projected *ProjectedToolAdapter) *ToolAdapter {
	return &ToolAdapter{
		Name:                 projected.SourceTool,
		Description:          projected.Description,
		Title:                defaultString(projected.Title, naming.HumanizeTitle(projected.SourceTool)),
		OriginalMethodName:   projected.BoundMethod,
		HasPayload:           projected.HasPayload,
		HasResult:            projected.HasResult,
		InputSchema:          projected.InputSchema,
		OutputSchema:         projected.OutputSchema,
		ExampleArguments:     projected.ExampleArguments,
		CanonicalExampleJSON: projected.ExampleArguments,
		Projected:            projected,
	}
}

func (g *adapterGenerator) buildToolAdapter(tool *mcpexpr.ToolExpr) (*ToolAdapter, error) {
	meta := g.originalMethodMeta(tool.Method.Name)
	adapter := &ToolAdapter{
		Name:               tool.Name,
		Description:        tool.Description,
		Title:              defaultString(tool.Title, naming.HumanizeTitle(tool.Name)),
		DiscoveryCategory:  tool.DiscoveryCategory,
		DiscoveryTags:      append([]string(nil), tool.DiscoveryTags...),
		DiscoveryKeywords:  append([]string(nil), tool.DiscoveryKeywords...),
		Icons:              iconDataFromExprs(tool.Icons),
		OriginalMethodName: codegen.Goify(tool.Method.Name, true),
		Meta:               mcpAnnotationEntries(meta),
		MetaJSON:           mcpDiscoveryMetaJSON(tool),
		AnnotationsJSON:    mcpAnnotationJSON(meta),
		HasPayload:         hasNonEmptyPayload(tool.Method.Payload),
		HasResult:          tool.Method.Result != nil,
		IsStreaming:        tool.Method.Stream == expr.ServerStreamKind,
	}
	g.populateToolStreamingData(adapter, tool)
	if err := g.populateToolPayloadData(adapter, tool); err != nil {
		return nil, err
	}
	if err := g.populateToolResultData(adapter, tool); err != nil {
		return nil, err
	}
	return adapter, nil
}

func (g *adapterGenerator) populateToolStreamingData(adapter *ToolAdapter, tool *mcpexpr.ToolExpr) {
	if !adapter.IsStreaming {
		return
	}
	adapter.StreamInterface = codegen.Goify(tool.Method.Name, true) + "ServerStream"
	adapter.StreamEventType = g.getTypeReference(tool.Method.StreamingResult)
}

func (g *adapterGenerator) populateToolPayloadData(adapter *ToolAdapter, tool *mcpexpr.ToolExpr) error {
	if !adapter.HasPayload {
		adapter.ExampleArguments = "{}"
		return nil
	}
	payload := tool.Method.Payload
	adapter.PayloadType = g.getTypeReference(payload)
	schema, err := shared.ToJSONSchema(payload)
	if err != nil {
		return fmt.Errorf("build schema for tool %q: %w", tool.Name, err)
	}
	adapter.InputSchema = schema
	req, enums, defaults := collectTopLevelValidations(payload)
	adapter.RequiredFields = req
	adapter.EnumFields = enums
	adapter.DefaultFields = defaults
	adapter.ExampleArguments = synthesizeCanonicalExample(payload)
	adapter.CanonicalExampleJSON = adapter.ExampleArguments
	adapter.UnionEnvelopes = collectUnionEnvelopes(payload)
	return nil
}

func (g *adapterGenerator) populateToolResultData(adapter *ToolAdapter, tool *mcpexpr.ToolExpr) error {
	if tool.Method.Result == nil {
		return nil
	}
	adapter.ResultType = g.getTypeReference(tool.Method.Result)
	if !isObjectBackedAttribute(tool.Method.Result) {
		return nil
	}
	schema, err := shared.ToJSONSchema(tool.Method.Result)
	if err != nil {
		return fmt.Errorf("build output schema for tool %q: %w", tool.Name, err)
	}
	adapter.OutputSchema = schema
	return nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func mcpDiscoveryMetaJSON(tool *mcpexpr.ToolExpr) string {
	if tool == nil {
		return ""
	}
	discovery := map[string]any{}
	if tool.DiscoveryCategory != "" {
		discovery["category"] = tool.DiscoveryCategory
	}
	if len(tool.DiscoveryTags) > 0 {
		discovery["tags"] = append([]string(nil), tool.DiscoveryTags...)
	}
	if len(tool.DiscoveryKeywords) > 0 {
		discovery["keywords"] = append([]string(nil), tool.DiscoveryKeywords...)
	}
	if len(tool.DiscoveryCallTemplateArgs) > 0 {
		discovery["call_template_arguments"] = cloneJSONMap(tool.DiscoveryCallTemplateArgs)
	}
	if len(discovery) == 0 {
		return ""
	}
	meta := map[string]any{
		"com.github.caliluke.loom-mcp/discovery": discovery,
	}
	raw, err := json.Marshal(meta, json.Deterministic(true))
	if err != nil {
		return ""
	}
	return string(raw)
}

func cloneJSONMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func isObjectBackedAttribute(attr *expr.AttributeExpr) bool {
	if attr == nil || attr.Type == nil {
		return false
	}
	switch actual := attr.Type.(type) {
	case *expr.Object:
		return true
	case expr.UserType:
		return isObjectBackedAttribute(actual.Attribute())
	default:
		return false
	}
}

func mcpAnnotationJSON(meta expr.MetaExpr) string {
	entries := mcpAnnotationEntries(meta)
	if len(entries) == 0 {
		return ""
	}
	normalized := make(map[string]any, len(entries))
	for _, entry := range entries {
		switch strings.ToLower(entry.Values[0]) {
		case "true":
			normalized[entry.Key] = true
		case "false":
			normalized[entry.Key] = false
		default:
			normalized[entry.Key] = entry.Values[0]
		}
	}
	if len(normalized) == 0 {
		return ""
	}
	data, err := json.Marshal(normalized, json.Deterministic(true))
	if err != nil {
		return ""
	}
	return string(data)
}

// mcpAnnotationEntries extracts MCP annotations from original method metadata.
func mcpAnnotationEntries(meta expr.MetaExpr) []AnnotationMetaEntry {
	if len(meta) == 0 {
		return nil
	}
	keys := []string{
		"readOnlyHint",
		"openWorldHint",
		"destructiveHint",
	}
	entries := make([]AnnotationMetaEntry, 0, len(keys))
	for _, key := range keys {
		values := meta["mcp:annotation:"+key]
		if len(values) == 0 {
			values = meta[key]
		}
		if len(values) == 0 {
			continue
		}
		entries = append(entries, AnnotationMetaEntry{
			Key:    key,
			Values: append([]string(nil), values...),
		})
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}

func (g *adapterGenerator) originalMethodMeta(name string) expr.MetaExpr {
	for _, method := range g.originalService.Methods {
		if method.Name == name {
			return method.Meta
		}
	}
	return nil
}

// collectTopLevelValidations extracts required fields and enum values for a top-level object payload
// buildResourceAdapters creates adapter data for resources.
func (g *adapterGenerator) buildResourceAdapters() ([]*ResourceAdapter, error) {
	adapters := make([]*ResourceAdapter, 0, len(g.mcp.Resources))

	for _, resource := range g.mcp.Resources {
		adapter, err := g.buildResourceAdapter(resource)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, adapter)
	}

	return adapters, nil
}

func (g *adapterGenerator) buildSkillDirectoryAdapters() []*SkillDirectoryAdapter {
	dirs := make([]*SkillDirectoryAdapter, 0, len(g.mcp.SkillDirectories))
	for _, dir := range g.mcp.SkillDirectories {
		dirs = append(dirs, &SkillDirectoryAdapter{Root: dir.Root})
	}
	return dirs
}

func (g *adapterGenerator) buildResourceAdapter(resource *mcpexpr.ResourceExpr) (*ResourceAdapter, error) {
	adapter := &ResourceAdapter{
		Name:               resource.Name,
		Description:        resource.Description,
		URI:                resource.URI,
		MimeType:           resource.MimeType,
		Icons:              iconDataFromExprs(resource.Icons),
		OriginalMethodName: codegen.Goify(resource.Method.Name, true),
		HasPayload:         hasNonEmptyPayload(resource.Method.Payload),
		HasResult:          resource.Method.Result != nil,
		Watchable:          resource.Watchable,
		IsStreaming:        resource.Method.Stream == expr.ServerStreamKind,
	}
	if adapter.IsStreaming {
		adapter.StreamInterface = codegen.Goify(resource.Method.Name, true) + "ServerStream"
		adapter.StreamEventType = g.getTypeReference(resource.Method.StreamingResult)
	}
	if err := g.populateResourcePayloadData(adapter, resource); err != nil {
		return nil, err
	}
	g.populateResourceResultData(adapter, resource)
	return adapter, nil
}

func (g *adapterGenerator) populateResourcePayloadData(adapter *ResourceAdapter, resource *mcpexpr.ResourceExpr) error {
	if !adapter.HasPayload {
		return nil
	}
	adapter.PayloadType = g.getTypeReference(resource.Method.Payload)
	queryFields, err := buildResourceQueryFields(resource.Method.Payload)
	if err != nil {
		return fmt.Errorf("build resource query fields for %q: %w", resource.Method.Name, err)
	}
	adapter.QueryFields = queryFields
	return nil
}

func (g *adapterGenerator) populateResourceResultData(adapter *ResourceAdapter, resource *mcpexpr.ResourceExpr) {
	if resource.Method.Result == nil {
		return
	}
	adapter.ResultType = g.getTypeReference(resource.Method.Result)
}

func hasNonEmptyPayload(attr *expr.AttributeExpr) bool {
	return attr != nil && attr.Type != expr.Empty
}

func oauthDataFromExpr(o *mcpexpr.OAuthExpr) *OAuthData {
	if o == nil {
		return nil
	}
	data := &OAuthData{
		AuthorizationServers:     append([]string(nil), o.AuthorizationServers...),
		ResourceIdentifier:       o.ResourceIdentifier,
		ResourceDocumentationURL: o.ResourceDocumentationURL,
		TrustProxyHeaders:        o.TrustProxyHeaders,
	}
	if len(o.BearerMethodsSupported) > 0 {
		data.BearerMethodsSupported = append([]string(nil), o.BearerMethodsSupported...)
	} else {
		data.BearerMethodsSupported = []string{"header"}
	}
	for _, scope := range o.Scopes {
		if scope == nil || scope.Name == "" {
			continue
		}
		data.Scopes = append(data.Scopes, OAuthScopeData{
			Name:        scope.Name,
			Description: scope.Description,
		})
	}
	return data
}

func iconDataFromExprs(icons []*mcpexpr.IconExpr) []*IconData {
	if len(icons) == 0 {
		return nil
	}
	out := make([]*IconData, 0, len(icons))
	for _, icon := range icons {
		if icon == nil {
			continue
		}
		out = append(out, &IconData{
			Source:   icon.Source,
			MIMEType: icon.MIMEType,
			Sizes:    append([]string(nil), icon.Sizes...),
			Theme:    icon.Theme,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// getTypeReference returns a Go type reference for an attribute
func (g *adapterGenerator) getTypeReference(attr *expr.AttributeExpr) string {
	// Service package alias used in adapter imports.
	svcAlias := codegen.SnakeCase(g.originalService.Name)
	// External user types should be qualified with their locator package alias.
	if ut, ok := attr.Type.(expr.UserType); ok && ut != nil {
		if loc := codegen.UserTypeLocation(ut); loc != nil && loc.PackageName() != "" {
			return g.scope.GoFullTypeRef(attr, loc.PackageName())
		}
	}
	// For composites and service-local user types, qualify nested refs with service alias.
	return g.scope.GoFullTypeRef(attr, svcAlias)
}
