// Package mcp defines the expression types used to represent MCP server
// configuration during Goa design evaluation. These types are populated during
// DSL execution and form the schema used for MCP protocol code generation.
package mcp

import (
	"encoding/json/v2"
	"errors"
	"net/url"
	"strings"

	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
)

var (
	errAbsoluteURLRequired      = errors.New("must be an absolute URL with scheme and host")
	errAuthorizationServerURL   = errors.New("must be an absolute URL with https scheme and host")
	errAuthorizationServerQuery = errors.New("must not contain a query")
	errURLFragment              = errors.New("must not contain a fragment")
)

const (
	// ToolSearchExactMatchNarrow suppresses weaker matches when a query exactly
	// matches a tool name or title.
	ToolSearchExactMatchNarrow = "narrow"
	// ToolSearchExactMatchBoost ranks exact matches highly but keeps lower
	// confidence matches eligible.
	ToolSearchExactMatchBoost = "boost"
	// ToolSearchExactMatchOff disables exact-match special handling.
	ToolSearchExactMatchOff = "off"
)

func urlParse(raw string) (*url.URL, error) {
	return url.Parse(raw)
}

type (
	// IconExpr defines one icon metadata entry exposed through MCP.
	IconExpr struct {
		eval.Expression

		// Source is the icon URI or data URI.
		Source string
		// MIMEType is the optional icon content type.
		MIMEType string
		// Sizes lists supported icon sizes.
		Sizes []string
		// Theme is the optional theme preference for the icon.
		Theme string
	}

	// MCPExpr defines MCP server configuration for a Goa service.
	MCPExpr struct {
		eval.Expression

		// Name is the MCP server name as advertised to MCP clients.
		Name string
		// Version is the server implementation version.
		Version string
		// Description provides a human-readable explanation of the
		// server's purpose.
		Description string
		// WebsiteURL is the optional documentation or home page URL for the
		// server implementation.
		WebsiteURL string
		// Icons is the optional icon metadata exposed for the server
		// implementation.
		Icons []*IconExpr
		// Transport is the transport mechanism (e.g., "jsonrpc",
		// "sse").
		Transport string
		// Capabilities defines which MCP capabilities this server
		// supports.
		Capabilities *CapabilitiesExpr
		// Tools is the collection of tool expressions exposed by this
		// server.
		Tools []*ToolExpr
		// Resources is the collection of resource expressions exposed
		// by this server.
		Resources []*ResourceExpr
		// SkillDirectories are filesystem roots scanned for agent skills
		// and exposed as skill:// MCP resources.
		SkillDirectories []*SkillDirectoryExpr
		// Prompts is the collection of static prompt expressions
		// exposed by this server.
		Prompts []*PromptExpr
		// OAuth is the optional OAuth 2.0 protected-resource configuration
		// that drives Protected Resource Metadata emission and the
		// WWW-Authenticate challenge. When nil, the server does not
		// advertise OAuth discovery.
		OAuth *OAuthExpr
		// ToolSearch is the optional design-time default policy for generated
		// progressive tool discovery.
		ToolSearch *ToolSearchExpr
		// Service is the Goa service expression this MCP server is
		// bound to.
		Service *expr.ServiceExpr
	}

	// ToolSearchExpr configures generated progressive discovery ranking
	// defaults for an MCP server.
	ToolSearchExpr struct {
		eval.Expression

		// DefaultMaxResults caps search results when runtime options and
		// request payloads do not set a positive max.
		DefaultMaxResults int
		// MinScore suppresses matches below this generated ranking score.
		MinScore int
		// ExactMatchMode controls what happens when a query exactly matches a
		// tool name or title.
		ExactMatchMode string
		// FuzzyNameMatching enables fuzzy ranking against tool names and titles.
		FuzzyNameMatching *bool
		// BroadFallback allows weaker metadata, description, parameter, and
		// schema matches when no strong name/title match exists.
		BroadFallback *bool
		// Weights customizes field weighting for generated ranking.
		Weights ToolSearchWeightsExpr
	}

	// ToolSearchWeightsExpr configures generated search ranking weights.
	ToolSearchWeightsExpr struct {
		Name        *int
		Title       *int
		Metadata    *int
		Description *int
		Parameters  *int
		FuzzyName   *int
	}

	// OAuthExpr describes the OAuth 2.0 protected-resource configuration
	// exposed by an MCP server. It backs the Protected Resource Metadata
	// document (RFC 9728) served at .well-known/oauth-protected-resource
	// and the WWW-Authenticate challenge emitted for unauthenticated
	// requests.
	OAuthExpr struct {
		// AuthorizationServers lists the OAuth 2.0 authorization server issuer
		// identifiers that can issue tokens for this resource. Each entry must use
		// HTTPS and have no query or fragment. Required; at least one entry.
		AuthorizationServers []string
		// Scopes documents the scopes the resource defines.
		Scopes []*ScopeExpr
		// ResourceIdentifier is the optional canonical audience URI
		// emitted as the "resource" field in PRM JSON. When empty, the
		// generated handler derives it from the incoming request.
		ResourceIdentifier string
		// BearerMethodsSupported enumerates the ways a client may
		// present a bearer token. Defaults to ["header"] at generation
		// time when empty.
		BearerMethodsSupported []string
		// ResourceDocumentationURL surfaces as resource_documentation
		// in the PRM document.
		ResourceDocumentationURL string
		// TrustProxyHeaders determines whether the generated server
		// consumes X-Forwarded-Proto / X-Forwarded-Host / Forwarded
		// headers when deriving the canonical resource URL or the
		// challenge origin. Default is false: forwarded headers are
		// ignored entirely, and origin is derived from r.Host + r.TLS.
		// Enable this only when the server sits behind a proxy the
		// operator fully controls and trusts — otherwise an attacker
		// with direct access can poison the PRM `resource` field.
		TrustProxyHeaders bool
	}

	// ScopeExpr documents one OAuth 2.0 scope advertised by an MCP
	// server.
	ScopeExpr struct {
		// Name is the scope token value.
		Name string
		// Description is the human-readable summary surfaced in PRM
		// JSON and in the WWW-Authenticate challenge.
		Description string
	}

	// SkillDirectoryExpr declares a filesystem root containing skill
	// directories. Each child directory with a SKILL.md file is exposed as
	// skill:// resources.
	SkillDirectoryExpr struct {
		eval.Expression

		// Root is the filesystem directory containing skill subdirectories.
		Root string
	}

	// CapabilitiesExpr defines which MCP protocol capabilities a server supports.
	CapabilitiesExpr struct {
		eval.Expression

		// EnableTools indicates whether the server exposes tool
		// invocation.
		EnableTools bool
		// EnableResources indicates whether the server exposes resource
		// access.
		EnableResources bool
		// EnablePrompts indicates whether the server exposes prompt
		// templates.
		EnablePrompts bool
	}

	// ToolExpr defines an MCP tool that the server exposes for invocation.
	ToolExpr struct {
		eval.Expression

		// Name is the unique identifier for this tool.
		Name string
		// Description provides a human-readable explanation of what the
		// tool does.
		Description string
		// Title is the optional human-readable display name for this tool.
		Title string
		// DiscoveryCategory is the optional category used by generated
		// progressive discovery search metadata.
		DiscoveryCategory string
		// DiscoveryTags are optional labels used by generated
		// progressive discovery search metadata.
		DiscoveryTags []string
		// DiscoveryKeywords are optional extra search terms used by
		// generated progressive discovery search metadata.
		DiscoveryKeywords []string
		// DiscoveryCallTemplateArgs are optional exemplar arguments included in
		// progressive discovery call_tool templates without changing validation.
		DiscoveryCallTemplateArgs map[string]any
		// Method is the Goa service method that implements this tool.
		Method *expr.MethodExpr
		// InputSchema defines the parameter schema for this tool.
		InputSchema *expr.AttributeExpr
		// Icons is the optional icon metadata exposed for this tool.
		Icons []*IconExpr
		// ExposedSurfaces records projection option use on method-level tools.
		ExposedSurfaces []string
		// MCPPlacementService records invalid placement use on method-level tools.
		MCPPlacementService string
		// MCPPlacementServer records invalid placement use on method-level tools.
		MCPPlacementServer string
	}

	// ResourceExpr defines an MCP resource that the server exposes for access.
	ResourceExpr struct {
		eval.Expression

		// Name is the unique identifier for this resource.
		Name string
		// Description provides a human-readable explanation of the
		// resource.
		Description string
		// URI is the resource identifier used for access.
		URI string
		// MimeType is the MIME type of the resource content.
		MimeType string
		// Method is the Goa service method that provides this resource.
		Method *expr.MethodExpr
		// Watchable indicates whether this resource supports change
		// notifications.
		Watchable bool
		// Icons is the optional icon metadata exposed for this resource.
		Icons []*IconExpr
	}

	// PromptExpr defines a static MCP prompt template exposed by the
	// server.
	PromptExpr struct {
		eval.Expression

		// Name is the unique identifier for this prompt.
		Name string
		// Description provides a human-readable explanation of the
		// prompt's purpose.
		Description string
		// Arguments defines the parameter schema for this prompt
		// template.
		Arguments *expr.AttributeExpr
		// Messages is the collection of message templates in this
		// prompt.
		Messages []*MessageExpr
		// Runtime optionally declares that this MCP prompt should also be
		// registered as a runtime prompt spec.
		Runtime *RuntimePromptExpr
		// Icons is the optional icon metadata exposed for this prompt.
		Icons []*IconExpr
	}

	// RuntimePromptExpr describes the runtime prompt spec generated from a
	// static MCP prompt declaration.
	RuntimePromptExpr struct {
		eval.Expression

		// AgentID identifies the owning runtime agent.
		AgentID string
		// Role identifies how the prompt is used at runtime.
		Role string
		// Version identifies the baseline runtime prompt version.
		Version string
	}

	// MessageExpr defines a single message within a prompt template.
	MessageExpr struct {
		eval.Expression

		// Role is the message sender role (e.g., "user", "assistant").
		Role string
		// Content is the message text content or template.
		Content string
	}

	// DynamicPromptExpr defines a dynamic prompt generated at runtime by a
	// service method.
	DynamicPromptExpr struct {
		eval.Expression

		// Name is the unique identifier for this dynamic prompt.
		Name string
		// Description provides a human-readable explanation of the prompt's
		// purpose.
		Description string
		// Method is the Goa service method that generates this prompt.
		Method *expr.MethodExpr
		// Icons is the optional icon metadata exposed for this prompt.
		Icons []*IconExpr
	}
)

// EvalName returns the name used for evaluation.
func (m *MCPExpr) EvalName() string {
	return "MCP server for " + m.Service.Name
}

const (
	// IconThemeLight declares that the icon is designed for light backgrounds.
	IconThemeLight = "light"
	// IconThemeDark declares that the icon is designed for dark backgrounds.
	IconThemeDark = "dark"
	// RuntimePromptRoleSystem identifies runtime system prompts.
	RuntimePromptRoleSystem = "system"
	// RuntimePromptRoleUser identifies runtime user prompts.
	RuntimePromptRoleUser = "user"
	// RuntimePromptRoleTool identifies runtime tool prompts.
	RuntimePromptRoleTool = "tool"
	// RuntimePromptRoleSynthesis identifies runtime synthesis prompts.
	RuntimePromptRoleSynthesis = "synthesis"

	promptRoleAssistant = "assistant"
)

// Finalize finalizes the MCP expression
func (m *MCPExpr) Finalize() {
	if m.Service != nil {
		m.Description = m.Service.Description
	}
	for _, resource := range m.Resources {
		if resource != nil && resource.Method != nil {
			resource.Description = resource.Method.Description
		}
	}
	if m.Transport == "" {
		m.Transport = "jsonrpc"
	}
	if m.Capabilities == nil {
		m.Capabilities = &CapabilitiesExpr{}
	}
	if len(m.Tools) > 0 {
		m.Capabilities.EnableTools = true
	}
	if len(m.Resources) > 0 || len(m.SkillDirectories) > 0 {
		m.Capabilities.EnableResources = true
	}
	hasDynamicPrompts := Root != nil && m.Service != nil && len(Root.DynamicPrompts[m.Service.Name]) > 0
	if len(m.Prompts) > 0 || hasDynamicPrompts {
		m.Capabilities.EnablePrompts = true
	}
}

// Validate validates the MCP expression
func (m *MCPExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if m.Name == "" {
		verr.Add(m, "MCP server name is required")
	}
	if m.Version == "" {
		verr.Add(m, "MCP server version is required")
	}
	mergeChildErrors(verr, m.Icons, iconValidator)
	mergeChildErrors(verr, m.SkillDirectories, skillDirectoryValidator)
	validateUniqueToolNames(verr, m.Tools)
	validateSingleToolPerMethod(verr, m.Tools)
	validateUniqueResourceNames(verr, m.Resources)
	validateUniqueResourceURIs(verr, m.Resources)
	validateUniquePromptNames(verr, m.Prompts)
	if m.OAuth != nil {
		mergeValidationError(verr, m.OAuth.Validate())
	}
	if m.ToolSearch != nil {
		mergeValidationError(verr, m.ToolSearch.Validate())
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

func iconValidator(icon *IconExpr) error { return icon.Validate() }

func skillDirectoryValidator(s *SkillDirectoryExpr) error {
	return s.Validate()
}

func validateUniqueToolNames(verr *eval.ValidationErrors, tools []*ToolExpr) {
	seen := make(map[string]*ToolExpr, len(tools))
	for _, tool := range tools {
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		if other, dup := seen[tool.Name]; dup {
			verr.Add(tool, "tool name %q duplicates tool %q declared for method %q", tool.Name, other.Name, methodName(other.Method))
			continue
		}
		seen[tool.Name] = tool
	}
}

func validateSingleToolPerMethod(verr *eval.ValidationErrors, tools []*ToolExpr) {
	seen := make(map[string]*ToolExpr, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.Method == nil || strings.TrimSpace(tool.Method.Name) == "" {
			continue
		}
		if other, dup := seen[tool.Method.Name]; dup {
			verr.Add(tool, "method %q declares multiple MCP tools that generate helper collisions: %q and %q", tool.Method.Name, other.Name, tool.Name)
			continue
		}
		seen[tool.Method.Name] = tool
	}
}

func validateUniqueResourceNames(verr *eval.ValidationErrors, resources []*ResourceExpr) {
	seen := make(map[string]*ResourceExpr, len(resources))
	for _, resource := range resources {
		if resource == nil || strings.TrimSpace(resource.Name) == "" {
			continue
		}
		if other, dup := seen[resource.Name]; dup {
			verr.Add(resource, "resource name %q duplicates resource declared for method %q", resource.Name, methodName(other.Method))
			continue
		}
		seen[resource.Name] = resource
	}
}

func validateUniqueResourceURIs(verr *eval.ValidationErrors, resources []*ResourceExpr) {
	seen := make(map[string]*ResourceExpr, len(resources))
	for _, resource := range resources {
		if resource == nil || strings.TrimSpace(resource.URI) == "" {
			continue
		}
		if other, dup := seen[resource.URI]; dup {
			verr.Add(resource, "resource URI %q duplicates resource %q declared for method %q", resource.URI, other.Name, methodName(other.Method))
			continue
		}
		seen[resource.URI] = resource
	}
}

func validateUniquePromptNames(verr *eval.ValidationErrors, prompts []*PromptExpr) {
	seen := make(map[string]*PromptExpr, len(prompts))
	for _, prompt := range prompts {
		if prompt == nil || strings.TrimSpace(prompt.Name) == "" {
			continue
		}
		if other, dup := seen[prompt.Name]; dup {
			verr.Add(prompt, "prompt name %q duplicates prompt declared in %s", prompt.Name, other.EvalName())
			continue
		}
		seen[prompt.Name] = prompt
	}
}

func methodName(method *expr.MethodExpr) string {
	if method == nil {
		return "<unknown>"
	}
	return method.Name
}

func mergeChildErrors[T any](dst *eval.ValidationErrors, items []T, validate func(T) error) {
	for _, item := range items {
		mergeValidationError(dst, validate(item))
	}
}

func mergeValidationError(dst *eval.ValidationErrors, err error) {
	if err == nil {
		return
	}
	var ve *eval.ValidationErrors
	if errors.As(err, &ve) {
		dst.Merge(ve)
	}
}

// Validate checks the OAuth protected-resource configuration against the
// constraints the generator and RFC 9728 require.
func (o *OAuthExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if len(o.AuthorizationServers) == 0 {
		verr.Add(o, "OAuth requires at least one AuthorizationServer")
	}
	for _, server := range o.AuthorizationServers {
		if err := validateAuthorizationServer(server); err != nil {
			verr.Add(o, "OAuth AuthorizationServer %q invalid: %s", server, err.Error())
		}
	}
	seenScope := make(map[string]struct{}, len(o.Scopes))
	for _, scope := range o.Scopes {
		if scope == nil {
			continue
		}
		if scope.Name == "" {
			verr.Add(o, "OAuth scope name is required")
			continue
		}
		if _, dup := seenScope[scope.Name]; dup {
			verr.Add(o, "OAuth scope %q declared more than once", scope.Name)
			continue
		}
		seenScope[scope.Name] = struct{}{}
	}
	for _, method := range o.BearerMethodsSupported {
		switch method {
		case "header", "body", "query":
		default:
			verr.Add(o, "OAuth BearerMethodsSupported must be header, body, or query; got %q", method)
		}
	}
	if id := o.ResourceIdentifier; id != "" {
		if err := validateResourceIdentifier(id); err != nil {
			verr.Add(o, "OAuth ResourceIdentifier invalid: %s", err.Error())
		}
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

// EvalName returns the expression name used in validation errors.
func (o *OAuthExpr) EvalName() string {
	return "MCP OAuth configuration"
}

func validateAuthorizationServer(server string) error {
	u, err := urlParse(server)
	if err != nil {
		return err
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return errAuthorizationServerURL
	}
	if u.RawQuery != "" || u.ForceQuery {
		return errAuthorizationServerQuery
	}
	if strings.Contains(server, "#") {
		return errURLFragment
	}
	return nil
}

func validateResourceIdentifier(id string) error {
	u, err := urlParse(id)
	if err != nil {
		return err
	}
	if u.Scheme == "" || u.Host == "" {
		return errAbsoluteURLRequired
	}
	if u.Fragment != "" {
		return errURLFragment
	}
	return nil
}

// EvalName returns the expression name used in validation errors.
func (t *ToolSearchExpr) EvalName() string {
	return "MCP ToolSearch"
}

// Validate validates the progressive tool discovery search policy.
func (t *ToolSearchExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if t.DefaultMaxResults < 0 {
		verr.Add(t, "ToolSearch DefaultMaxResults must be non-negative")
	}
	if t.MinScore < 0 {
		verr.Add(t, "ToolSearch MinScore must be non-negative")
	}
	switch t.ExactMatchMode {
	case "", ToolSearchExactMatchNarrow, ToolSearchExactMatchBoost, ToolSearchExactMatchOff:
	default:
		verr.Add(t, "ToolSearch ExactMatchMode must be narrow, boost, or off; got %q", t.ExactMatchMode)
	}
	validateToolSearchWeight(verr, t, "Name", t.Weights.Name)
	validateToolSearchWeight(verr, t, "Title", t.Weights.Title)
	validateToolSearchWeight(verr, t, "Metadata", t.Weights.Metadata)
	validateToolSearchWeight(verr, t, "Description", t.Weights.Description)
	validateToolSearchWeight(verr, t, "Parameters", t.Weights.Parameters)
	validateToolSearchWeight(verr, t, "FuzzyName", t.Weights.FuzzyName)
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

func validateToolSearchWeight(verr *eval.ValidationErrors, expr eval.Expression, name string, weight *int) {
	if weight != nil && *weight < 0 {
		verr.Add(expr, "ToolSearch %s weight must be non-negative", name)
	}
}

// Validate validates a tool expression
func (t *ToolExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if t.Name == "" {
		verr.Add(t, "tool name is required")
	}
	if t.Description == "" {
		verr.Add(t, "tool description is required")
	}
	for name, value := range t.DiscoveryCallTemplateArgs {
		if strings.TrimSpace(name) == "" {
			verr.Add(t, "ToolDiscoveryCallTemplateArg field name must be non-empty")
			continue
		}
		if _, err := json.Marshal(value); err != nil {
			verr.Add(t, "ToolDiscoveryCallTemplateArg %q must be JSON-marshalable: %v", name, err)
		}
	}
	for _, icon := range t.Icons {
		if err := icon.Validate(); err != nil {
			var ve *eval.ValidationErrors
			if errors.As(err, &ve) {
				verr.Merge(ve)
			}
		}
	}
	for _, surface := range t.ExposedSurfaces {
		if surface == "agent_runtime" {
			verr.Add(t, "Expose(AgentRuntime) is invalid on method-level MCP tools")
		}
	}
	if t.MCPPlacementService != "" || t.MCPPlacementServer != "" {
		verr.Add(t, "MCPPlacement is invalid on method-level MCP tools")
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

// Validate validates a resource expression
func (r *ResourceExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if r.Name == "" {
		verr.Add(r, "resource name is required")
	}
	if r.URI == "" {
		verr.Add(r, "resource URI is required")
	}
	for _, icon := range r.Icons {
		if err := icon.Validate(); err != nil {
			var ve *eval.ValidationErrors
			if errors.As(err, &ve) {
				verr.Merge(ve)
			}
		}
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

// Validate validates a skill directory expression.
func (s *SkillDirectoryExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if s.Root == "" {
		verr.Add(s, "skill directory root is required")
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

// Validate validates a prompt expression
func (p *PromptExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if p.Name == "" {
		verr.Add(p, "prompt name is required")
	}
	if len(p.Messages) == 0 {
		verr.Add(p, "prompt must have at least one message")
	}

	for _, icon := range p.Icons {
		if err := icon.Validate(); err != nil {
			var ve *eval.ValidationErrors
			if errors.As(err, &ve) {
				verr.Merge(ve)
			}
		}
	}
	if p.Runtime != nil {
		if err := p.Runtime.Validate(); err != nil {
			var ve *eval.ValidationErrors
			if errors.As(err, &ve) {
				verr.Merge(ve)
			}
		}
		if len(p.Messages) != 1 {
			verr.Add(p, "runtime prompt %q must have exactly one message", p.Name)
		}
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

// Validate validates a dynamic prompt expression.
func (d *DynamicPromptExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if d.Name == "" {
		verr.Add(d, "dynamic prompt name is required")
	}
	mergeChildErrors(verr, d.Icons, iconValidator)
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

// Validate validates a static prompt message expression.
func (m *MessageExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	switch m.Role {
	case RuntimePromptRoleUser, promptRoleAssistant:
	default:
		verr.Add(m, "prompt message role must be user or assistant")
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

// Validate validates a runtime prompt expression.
func (r *RuntimePromptExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if r.AgentID == "" {
		verr.Add(r, "runtime prompt agent id is required")
	}
	switch r.Role {
	case RuntimePromptRoleSystem, RuntimePromptRoleUser, RuntimePromptRoleTool, RuntimePromptRoleSynthesis:
	default:
		verr.Add(r, "runtime prompt role must be system, user, tool, or synthesis")
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

// Validate validates icon metadata.
func (i *IconExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if i.Source == "" {
		verr.Add(i, "icon source is required")
	}
	switch i.Theme {
	case "", IconThemeLight, IconThemeDark:
	default:
		verr.Add(i, "icon theme must be empty, light, or dark")
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

// EvalName returns the name used for evaluation.
func (c *CapabilitiesExpr) EvalName() string {
	return "MCP capabilities"
}

// EvalName returns the name used for evaluation.
func (i *IconExpr) EvalName() string {
	return "MCP icon"
}

// EvalName returns the name used for evaluation.
func (t *ToolExpr) EvalName() string {
	return "MCP tool " + t.Name
}

// EvalName returns the name used for evaluation.
func (r *ResourceExpr) EvalName() string {
	return "MCP resource " + r.Name
}

// EvalName returns the name used for evaluation.
func (s *SkillDirectoryExpr) EvalName() string {
	return "MCP skill directory"
}

// EvalName returns the name used for evaluation.
func (p *PromptExpr) EvalName() string {
	return "MCP prompt " + p.Name
}

// EvalName returns the name used for evaluation.
func (m *MessageExpr) EvalName() string {
	return "MCP message"
}

// EvalName returns the name used for evaluation.
func (r *RuntimePromptExpr) EvalName() string {
	return "runtime prompt"
}

// EvalName returns the name used for evaluation.
func (d *DynamicPromptExpr) EvalName() string {
	return "MCP dynamic prompt " + d.Name
}
