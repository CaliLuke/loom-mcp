package codegen

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/CaliLuke/loom-mcp/codegen/shared"
	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
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
		ServiceName         string
		ServiceGoName       string
		MCPServiceName      string
		MCPName             string
		MCPVersion          string
		WebsiteURL          string
		Icons               []*IconData
		ProtocolVersion     string
		Package             string
		MCPPackage          string
		ServiceJSONRPCAlias string
		ImportPath          string
		Tools               []*ToolAdapter
		Resources           []*ResourceAdapter
		StaticPrompts       []*StaticPromptAdapter
		DynamicPrompts      []*DynamicPromptAdapter
		Notifications       []*NotificationAdapter
		Subscriptions       []*SubscriptionAdapter
		// Streaming flags derived from original service DSL
		ToolsCallStreaming bool
		// Derived flags
		HasWatchableResources bool
		NeedsMCPClient        bool
		NeedsOriginalClient   bool
		NeedsQueryFormatting  bool

		Register     *RegisterData
		ClientCaller *ClientCallerData
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

	ClientCallerData struct {
		MCPImportPath string
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
		Icons              []*IconData
		OriginalMethodName string
		Meta               []AnnotationMetaEntry
		AnnotationsJSON    string
		HasPayload         bool
		HasResult          bool
		PayloadType        string
		ResultType         string
		InputSchema        string
		IsStreaming        bool
		StreamInterface    string
		StreamEventType    string
		// Simple validations (top-level only)
		RequiredFields []string
		EnumFields     map[string][]string
		EnumFieldsPtr  map[string]bool
		DefaultFields  []DefaultField
		// ExampleArguments contains a minimal valid JSON for tool arguments
		ExampleArguments string
	}

	// DefaultField describes a top-level payload field default assignment.
	DefaultField struct {
		Name    string
		GoName  string
		Literal string
		Kind    string
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

	// resourceQueryFieldDefinition captures one flattened top-level resource
	// query field together with the presence rules implied by the Goa payload.
	resourceQueryFieldDefinition struct {
		Attribute        *expr.AttributeExpr
		Required         bool
		PrimitivePointer bool
	}

	// StaticPromptAdapter represents a static prompt
	StaticPromptAdapter struct {
		Name        string
		Description string
		Icons       []*IconData
		Messages    []*PromptMessageAdapter
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
	}

	// NotificationAdapter represents a notification mapping
	NotificationAdapter struct {
		Name               string
		Description        string
		OriginalMethodName string
		HasMessage         bool
		MessagePointer     bool
		HasData            bool
	}

	// SubscriptionAdapter represents a subscription mapping
	SubscriptionAdapter struct {
		ResourceName       string
		ResourceURI        string
		OriginalMethodName string
	}

	// adapterGenerator generates the adapter layer between MCP and the original service
	adapterGenerator struct {
		genpkg          string
		originalService *expr.ServiceExpr
		mcp             *mcpexpr.MCPExpr
		mapping         *ServiceMethodMapping
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
)

// newAdapterGenerator creates a new adapter generator
func newAdapterGenerator(
	genpkg string,
	svc *expr.ServiceExpr,
	mcp *mcpexpr.MCPExpr,
	mapping *ServiceMethodMapping,
) *adapterGenerator {
	return &adapterGenerator{
		genpkg:          genpkg,
		originalService: svc,
		mcp:             mcp,
		mapping:         mapping,
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
		ServiceName:         g.originalService.Name,
		ServiceGoName:       codegen.Goify(g.originalService.Name, true),
		MCPServiceName:      g.originalService.Name,
		MCPName:             g.mcp.Name,
		MCPVersion:          g.mcp.Version,
		WebsiteURL:          g.mcp.WebsiteURL,
		Icons:               iconDataFromExprs(g.mcp.Icons),
		ProtocolVersion:     g.mcp.ProtocolVersion,
		Package:             codegen.SnakeCase(g.originalService.Name),
		MCPPackage:          "mcp" + strings.ToLower(codegen.Goify(g.originalService.Name, false)),
		ServiceJSONRPCAlias: codegen.SnakeCase(g.originalService.Name) + "jsonrpc",
		ImportPath:          g.genpkg,
		Tools:               tools,
		Resources:           resources,
	}
}

func (g *adapterGenerator) populateAdapterDataCollections(data *AdapterData) {
	data.DynamicPrompts = g.buildDynamicPromptAdapters()
	data.Notifications = g.buildNotificationAdapters()
	data.Subscriptions = g.buildSubscriptionAdapters()
	data.StaticPrompts = g.buildStaticPrompts()
}

func (g *adapterGenerator) populateAdapterDataFlags(data *AdapterData) {
	data.ToolsCallStreaming = true
	data.HasWatchableResources = hasWatchableResources(data.Resources)
	data.NeedsMCPClient = adapterDataNeedsMCPClient(data)
	data.NeedsOriginalClient = len(data.DynamicPrompts) > 0 || adapterDataNeedsOriginalClient(data.Tools, data.Resources)
	data.NeedsQueryFormatting = adapterDataNeedsQueryFormatting(data.Resources)
}

func (g *adapterGenerator) populateAdapterHelperData(data *AdapterData) {
	data.Register = g.buildRegisterData(data)
	data.ClientCaller = g.buildClientCallerData(data, g.genpkg)
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

func (g *adapterGenerator) buildClientCallerData(data *AdapterData, genpkg string) *ClientCallerData {
	if data.Register == nil {
		return nil
	}
	svcName := codegen.SnakeCase(g.originalService.Name)
	importPath := path.Join(genpkg, "mcp_"+svcName)
	return &ClientCallerData{
		MCPImportPath: importPath,
	}
}

// adapterDataNeedsOriginalClient reports whether any generated endpoint must
// decode an MCP response through the original JSON-RPC client.
func adapterDataNeedsOriginalClient(tools []*ToolAdapter, resources []*ResourceAdapter) bool {
	for _, tool := range tools {
		if tool.HasResult {
			return true
		}
	}
	for _, resource := range resources {
		if resource.HasResult {
			return true
		}
	}
	return false
}

func adapterDataNeedsMCPClient(data *AdapterData) bool {
	return len(data.Tools) > 0 ||
		len(data.Resources) > 0 ||
		len(data.DynamicPrompts) > 0 ||
		len(data.Notifications) > 0
}

func hasWatchableResources(resources []*ResourceAdapter) bool {
	for _, resource := range resources {
		if resource.Watchable {
			return true
		}
	}
	return false
}

// adapterDataNeedsQueryFormatting reports whether resource query emission needs
// strconv-based formatting for non-string primitive query values.
func adapterDataNeedsQueryFormatting(resources []*ResourceAdapter) bool {
	for _, resource := range resources {
		for _, field := range resource.QueryFields {
			if field.FormatKind != resourceQueryFormatString {
				return true
			}
		}
	}
	return false
}

// buildToolAdapters creates adapter data for tools.
func (g *adapterGenerator) buildToolAdapters() ([]*ToolAdapter, error) {
	adapters := make([]*ToolAdapter, 0, len(g.mcp.Tools))

	for _, tool := range g.mcp.Tools {
		adapter, err := g.buildToolAdapter(tool)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, adapter)
	}

	return adapters, nil
}

func (g *adapterGenerator) buildToolAdapter(tool *mcpexpr.ToolExpr) (*ToolAdapter, error) {
	meta := g.originalMethodMeta(tool.Method.Name)
	adapter := &ToolAdapter{
		Name:               tool.Name,
		Description:        tool.Description,
		Icons:              iconDataFromExprs(tool.Icons),
		OriginalMethodName: codegen.Goify(tool.Method.Name, true),
		Meta:               mcpAnnotationEntries(meta),
		AnnotationsJSON:    mcpAnnotationJSON(meta),
		HasPayload:         hasNonEmptyPayload(tool.Method.Payload),
		HasResult:          tool.Method.Result != nil,
		IsStreaming:        tool.Method.Stream == expr.ServerStreamKind,
	}
	g.populateToolStreamingData(adapter, tool)
	if err := g.populateToolPayloadData(adapter, tool); err != nil {
		return nil, err
	}
	g.populateToolResultData(adapter, tool)
	return adapter, nil
}

func (g *adapterGenerator) populateToolStreamingData(adapter *ToolAdapter, tool *mcpexpr.ToolExpr) {
	if !adapter.IsStreaming {
		return
	}
	adapter.StreamInterface = codegen.Goify(tool.Method.Name, true) + "ServerStream"
	adapter.StreamEventType = codegen.Goify(tool.Method.Name, true) + "Event"
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
	req, enums, enumPtr, defaults := collectTopLevelValidations(payload)
	adapter.RequiredFields = req
	adapter.EnumFields = enums
	adapter.EnumFieldsPtr = enumPtr
	adapter.DefaultFields = defaults
	adapter.ExampleArguments = buildExampleJSON(payload)
	return nil
}

func (g *adapterGenerator) populateToolResultData(adapter *ToolAdapter, tool *mcpexpr.ToolExpr) {
	if tool.Method.Result == nil {
		return
	}
	adapter.ResultType = g.getTypeReference(tool.Method.Result)
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
	data, err := json.Marshal(normalized)
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

// buildNotificationAdapters creates adapter data for notifications
func (g *adapterGenerator) buildNotificationAdapters() []*NotificationAdapter {
	adapters := make([]*NotificationAdapter, 0)
	if g.mcp != nil {
		for _, n := range g.mcp.Notifications {
			fields := collectNotificationPayloadFields(n.Method.Payload)
			messageField, hasMessage := fields["message"]
			_, hasData := fields["data"]
			adapters = append(adapters, &NotificationAdapter{
				Name:               n.Name,
				Description:        n.Description,
				OriginalMethodName: codegen.Goify(n.Method.Name, true),
				HasMessage:         hasMessage,
				MessagePointer:     hasMessage && messageField.PrimitivePointer,
				HasData:            hasData,
			})
		}
	}
	return adapters
}

// buildSubscriptionAdapters creates adapter data for subscriptions
func (g *adapterGenerator) buildSubscriptionAdapters() []*SubscriptionAdapter {
	adapters := make([]*SubscriptionAdapter, 0)
	if g.mcp != nil {
		for _, s := range g.mcp.Subscriptions {
			adapters = append(adapters, &SubscriptionAdapter{
				ResourceName:       s.ResourceName,
				OriginalMethodName: codegen.Goify(s.Method.Name, true),
			})
		}
	}
	return adapters
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
