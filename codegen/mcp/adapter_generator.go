package codegen

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"goa.design/goa-ai/codegen/shared"
	mcpexpr "goa.design/goa-ai/expr/mcp"
	"goa.design/goa/v3/codegen"
	"goa.design/goa/v3/expr"
)

type (
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
	req, enums, enumPtr, defaults := g.collectTopLevelValidations(payload)
	adapter.RequiredFields = req
	adapter.EnumFields = enums
	adapter.EnumFieldsPtr = enumPtr
	adapter.DefaultFields = defaults
	adapter.ExampleArguments = g.buildExampleJSON(payload)
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
func (g *adapterGenerator) collectTopLevelValidations(
	attr *expr.AttributeExpr,
) ([]string, map[string][]string, map[string]bool, []DefaultField) {
	if attr == nil || attr.Type == nil || attr.Type == expr.Empty {
		return nil, nil, nil, nil
	}
	// Unwrap user type
	if ut, ok := attr.Type.(expr.UserType); ok {
		return g.collectTopLevelValidations(ut.Attribute())
	}
	obj, ok := attr.Type.(*expr.Object)
	if !ok {
		return nil, nil, nil, nil
	}
	req := []string{}
	enums := map[string][]string{}
	enumPtr := map[string]bool{}
	defaults := []DefaultField{}
	fields, enums, defaults := collectTopLevelValidationFields(obj)
	if attr.Validation != nil && len(attr.Validation.Required) > 0 {
		for _, name := range attr.Validation.Required {
			if fa, ok := fields[name]; ok {
				// Only require string fields here (simple non-empty check)
				if pk, okp := fa.Type.(expr.Primitive); okp && pk.Kind() == expr.StringKind {
					req = append(req, name)
				}
			}
		}
	}
	// Determine pointer-ness for enum fields: string enum fields not required are pointers
	reqSet := map[string]struct{}{}
	if attr.Validation != nil {
		for _, n := range attr.Validation.Required {
			reqSet[n] = struct{}{}
		}
	}
	for n := range enums {
		_, isReq := reqSet[n]
		hasDefault := fields[n] != nil && fields[n].DefaultValue != nil
		enumPtr[n] = !isReq && !hasDefault
	}
	return req, enums, enumPtr, defaults
}

func collectTopLevelValidationFields(obj *expr.Object) (map[string]*expr.AttributeExpr, map[string][]string, []DefaultField) {
	fields := map[string]*expr.AttributeExpr{}
	enums := map[string][]string{}
	defaults := []DefaultField{}
	for _, nat := range *obj {
		fields[nat.Name] = nat.Attribute
		if nat.Attribute.DefaultValue != nil {
			if def, ok := topLevelDefaultField(nat.Name, nat.Attribute); ok {
				defaults = append(defaults, def)
			}
		}
		if vals := collectEnumValues(nat.Attribute); len(vals) > 0 {
			enums[nat.Name] = vals
		}
	}
	return fields, enums, defaults
}

func collectEnumValues(attr *expr.AttributeExpr) []string {
	if attr == nil || attr.Validation == nil || len(attr.Validation.Values) == 0 {
		return nil
	}
	vals := make([]string, 0, len(attr.Validation.Values))
	for _, v := range attr.Validation.Values {
		vals = append(vals, fmt.Sprint(v))
	}
	if len(vals) == 0 {
		return nil
	}
	return vals
}

func topLevelDefaultField(name string, attr *expr.AttributeExpr) (DefaultField, bool) {
	if attr == nil || attr.Type == nil || attr.DefaultValue == nil {
		return DefaultField{}, false
	}
	goName := codegen.Goify(name, true)
	actual := attr.Type
	if ut, ok := actual.(expr.UserType); ok {
		actual = ut.Attribute().Type
	}
	switch actual {
	case expr.String:
		def, ok := attr.DefaultValue.(string)
		if !ok {
			return DefaultField{}, false
		}
		return DefaultField{Name: name, GoName: goName, Literal: fmt.Sprintf("%q", def), Kind: "string"}, true
	case expr.Boolean:
		def, ok := attr.DefaultValue.(bool)
		if !ok {
			return DefaultField{}, false
		}
		return DefaultField{Name: name, GoName: goName, Literal: fmt.Sprintf("%t", def), Kind: "bool"}, true
	case expr.Int, expr.Int32, expr.Int64, expr.UInt, expr.UInt32, expr.UInt64:
		return DefaultField{Name: name, GoName: goName, Literal: fmt.Sprintf("%v", attr.DefaultValue), Kind: "int"}, true
	default:
		return DefaultField{}, false
	}
}

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

// buildResourceQueryFields computes the statically known resource query plan so
// the template can emit direct query assembly without rediscovering payload
// structure at runtime.
func buildResourceQueryFields(payload *expr.AttributeExpr) ([]*ResourceQueryField, error) {
	definitions := collectResourceQueryFieldDefinitions(payload)
	if len(definitions) == 0 {
		return nil, fmt.Errorf(
			"payload must define at least one top-level primitive or array-of-primitive query field",
		)
	}
	return resourceQueryFieldPlan(definitions)
}

func collectResourceQueryFieldDefinitions(payload *expr.AttributeExpr) map[string]resourceQueryFieldDefinition {
	definitions := make(map[string]resourceQueryFieldDefinition)
	collectResourceQueryFields(payload, payload, definitions, make(map[string]struct{}))
	return definitions
}

func resourceQueryFieldPlan(definitions map[string]resourceQueryFieldDefinition) ([]*ResourceQueryField, error) {
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]*ResourceQueryField, 0, len(names))
	for _, name := range names {
		field, err := newResourceQueryField(name, definitions[name])
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// collectResourceQueryFields flattens the top-level resource payload across
// direct fields, bases, and references so generated query assembly preserves
// the original payload surface without runtime rediscovery.
func collectResourceQueryFields(
	root *expr.AttributeExpr,
	att *expr.AttributeExpr,
	fields map[string]resourceQueryFieldDefinition,
	seen map[string]struct{},
) {
	if att == nil || att.Type == nil {
		return
	}
	hash := att.Type.Hash()
	if _, ok := seen[hash]; ok {
		return
	}
	seen[hash] = struct{}{}
	for _, base := range att.Bases {
		collectResourceQueryFields(root, attributeDataType(base), fields, seen)
	}
	for _, ref := range att.References {
		collectResourceQueryFields(root, attributeDataType(ref), fields, seen)
	}
	object := expr.AsObject(att.Type)
	if object == nil {
		return
	}
	for _, named := range *object {
		required := att.IsRequired(named.Name) || root.IsRequired(named.Name)
		fields[named.Name] = resourceQueryFieldDefinition{
			Attribute:        named.Attribute,
			Required:         required,
			PrimitivePointer: !required && att.IsPrimitivePointer(named.Name, true),
		}
	}
}

// newResourceQueryField converts one flattened payload field into a concrete
// query-rendering plan for the client adapter template.
func newResourceQueryField(name string, definition resourceQueryFieldDefinition) (*ResourceQueryField, error) {
	fieldName := codegen.Goify(name, true)
	if array := expr.AsArray(definition.Attribute.Type); array != nil {
		formatKind, err := resourceQueryFormatKind(name, array.ElemType.Type)
		if err != nil {
			return nil, err
		}
		return &ResourceQueryField{
			QueryKey:       name,
			GuardExpr:      fmt.Sprintf("len(payload.%s) > 0", fieldName),
			CollectionExpr: fmt.Sprintf("payload.%s", fieldName),
			ValueExpr:      "value",
			FormatKind:     formatKind,
			Repeated:       true,
		}, nil
	}

	formatKind, err := resourceQueryFormatKind(name, definition.Attribute.Type)
	if err != nil {
		return nil, err
	}
	field := &ResourceQueryField{
		QueryKey:       name,
		ValueExpr:      fmt.Sprintf("payload.%s", fieldName),
		FormatKind:     formatKind,
		CollectionExpr: "",
	}
	if definition.Required {
		return field, nil
	}
	if definition.PrimitivePointer {
		field.GuardExpr = fmt.Sprintf("payload.%s != nil", fieldName)
		field.ValueExpr = fmt.Sprintf("*payload.%s", fieldName)
		return field, nil
	}
	field.GuardExpr = resourceQueryZeroGuardExpr(formatKind, fieldName)
	return field, nil
}

// attributeDataType recovers the full attribute metadata for base and reference
// types when they are modeled as named user types.
func attributeDataType(dt expr.DataType) *expr.AttributeExpr {
	if userType, ok := dt.(expr.UserType); ok {
		return userType.Attribute()
	}
	return &expr.AttributeExpr{Type: dt}
}

func hasNonEmptyPayload(attr *expr.AttributeExpr) bool {
	return attr != nil && attr.Type != expr.Empty
}

// resourceQueryFormatKind classifies one supported scalar query value so the
// template can emit direct string formatting without runtime JSON marshalling.
func resourceQueryFormatKind(fieldName string, dt expr.DataType) (string, error) {
	underlying := resourceQueryUnderlyingType(dt)
	if array := expr.AsArray(underlying); array != nil {
		return "", fmt.Errorf(
			`field %q uses nested array query values; expected primitive or array of primitive values`,
			fieldName,
		)
	}
	if !expr.IsPrimitive(underlying) {
		return "", fmt.Errorf(
			`field %q uses unsupported resource query type %q; expected primitive or array of primitive values`,
			fieldName,
			underlying.Name(),
		)
	}
	switch underlying.Kind() {
	case expr.StringKind:
		return resourceQueryFormatString, nil
	case expr.BooleanKind:
		return resourceQueryFormatBool, nil
	case expr.IntKind, expr.Int32Kind, expr.Int64Kind:
		return resourceQueryFormatInt, nil
	case expr.UIntKind, expr.UInt32Kind, expr.UInt64Kind:
		return resourceQueryFormatUint, nil
	case expr.Float32Kind:
		return resourceQueryFormatFloat32, nil
	case expr.Float64Kind:
		return resourceQueryFormatFloat64, nil
	case expr.BytesKind,
		expr.ArrayKind,
		expr.ObjectKind,
		expr.MapKind,
		expr.UnionKind,
		expr.UserTypeKind,
		expr.ResultTypeKind,
		expr.AnyKind:
		return "", fmt.Errorf(
			`field %q uses unsupported resource query type %q; expected string, bool, int, uint, float, or arrays of those values`,
			fieldName,
			underlying.Name(),
		)
	}
	return "", fmt.Errorf(
		`field %q uses unsupported resource query type %q; expected string, bool, int, uint, float, or arrays of those values`,
		fieldName,
		underlying.Name(),
	)
}

// resourceQueryZeroGuardExpr returns the direct zero-value guard for optional
// non-pointer scalar query fields.
func resourceQueryZeroGuardExpr(formatKind string, fieldName string) string {
	switch formatKind {
	case resourceQueryFormatString:
		return fmt.Sprintf(`payload.%s != ""`, fieldName)
	case resourceQueryFormatBool:
		return fmt.Sprintf("payload.%s", fieldName)
	default:
		return fmt.Sprintf("payload.%s != 0", fieldName)
	}
}

// resourceQueryUnderlyingType resolves aliases so query-field guard selection
// follows the concrete runtime kind that Goa will generate.
func resourceQueryUnderlyingType(dt expr.DataType) expr.DataType {
	switch actual := dt.(type) {
	case *expr.UserTypeExpr:
		return resourceQueryUnderlyingType(actual.Type)
	case *expr.ResultTypeExpr:
		return resourceQueryUnderlyingType(actual.Type)
	default:
		return actual
	}
}

// buildDynamicPromptAdapters creates adapter data for dynamic prompts
func (g *adapterGenerator) buildDynamicPromptAdapters() []*DynamicPromptAdapter {
	var adapters []*DynamicPromptAdapter

	if mcpexpr.Root != nil {
		dynamicPrompts := mcpexpr.Root.DynamicPrompts[g.originalService.Name]
		for _, dp := range dynamicPrompts {
			// Check if payload is Empty type (added by Goa during Finalize)
			hasRealPayload := dp.Method.Payload != nil && dp.Method.Payload.Type != expr.Empty

			adapter := &DynamicPromptAdapter{
				Name:               dp.Name,
				Description:        dp.Description,
				OriginalMethodName: codegen.Goify(dp.Method.Name, true),
				HasPayload:         hasRealPayload,
			}

			// Set payload type reference only for real payloads
			if hasRealPayload {
				adapter.PayloadType = g.getTypeReference(dp.Method.Payload)
				adapter.Arguments = g.promptArgsFromPayload(dp.Method.Payload)
				adapter.ExampleArguments = g.buildExampleJSON(dp.Method.Payload)
			} else {
				adapter.ExampleArguments = "{}"
			}

			// Set result type reference if present
			if dp.Method.Result != nil {
				adapter.ResultType = g.getTypeReference(dp.Method.Result)
			}

			adapters = append(adapters, adapter)
		}
	}

	return adapters
}

// buildExampleJSON produces a minimal valid JSON string for the given payload attribute.
// It prioritizes required fields and uses enum defaults when available.
func (g *adapterGenerator) buildExampleJSON(attr *expr.AttributeExpr) string {
	if attr == nil || attr.Type == nil || attr.Type == expr.Empty {
		return "{}"
	}
	// Use Goa's example generator with a deterministic randomizer for stable output
	r := &expr.ExampleGenerator{Randomizer: expr.NewDeterministicRandomizer()}
	v := attr.Example(r)
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// promptArgsFromPayload builds a flat list of prompt arguments from a payload attribute (top-level only)
func (g *adapterGenerator) promptArgsFromPayload(attr *expr.AttributeExpr) []PromptArg {
	if attr == nil || attr.Type == nil || attr.Type == expr.Empty {
		return nil
	}
	// Unwrap user type
	if ut, ok := attr.Type.(expr.UserType); ok {
		return g.promptArgsFromPayload(ut.Attribute())
	}
	obj, ok := attr.Type.(*expr.Object)
	if !ok {
		return nil
	}
	// Pre-allocate based on number of top-level fields
	out := make([]PromptArg, 0, len(*obj))
	// Build required set
	required := map[string]struct{}{}
	if attr.Validation != nil {
		for _, n := range attr.Validation.Required {
			required[n] = struct{}{}
		}
	}
	for _, nat := range *obj {
		name := nat.Name
		desc := ""
		if nat.Attribute != nil && nat.Attribute.Description != "" {
			desc = nat.Attribute.Description
		}
		_, req := required[name]
		out = append(out, PromptArg{Name: name, Description: desc, Required: req})
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

// buildStaticPrompts creates data for static prompts
func (g *adapterGenerator) buildStaticPrompts() []*StaticPromptAdapter {
	prompts := make([]*StaticPromptAdapter, 0, len(g.mcp.Prompts))

	for _, prompt := range g.mcp.Prompts {
		adapter := &StaticPromptAdapter{
			Name:        prompt.Name,
			Description: prompt.Description,
			Messages:    make([]*PromptMessageAdapter, len(prompt.Messages)),
		}

		for i, msg := range prompt.Messages {
			adapter.Messages[i] = &PromptMessageAdapter{
				Role:    msg.Role,
				Content: msg.Content,
			}
		}

		prompts = append(prompts, adapter)
	}

	return prompts
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
