package dsl

import (
	"strings"

	_ "github.com/CaliLuke/loom-mcp/v2/codegen/mcp" // Registers the MCP codegen plugin with Goa
	exprmcp "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
)

// MCP enables Model Context Protocol (MCP) support for the current service.
// It configures the service to expose tools, resources, and prompts via the MCP
// protocol. Once enabled, use Resource, Tool (in Method context), and related
// DSL functions within service methods to define MCP capabilities.
//
// MCP must appear in a Service expression.
//
// MCP takes two required arguments and optional configuration functions:
//   - name: the MCP server name (used in MCP handshake)
//   - version: the server version string
//   - opts: optional configuration functions such as WebsiteURL
//
// Example:
//
//	Service("calculator", func() {
//	    MCP("calc", "1.0.0", WebsiteURL("https://example.com/calc"))
//	    Method("add", func() {
//	        Payload(func() {
//	            Attribute("a", Int)
//	            Attribute("b", Int)
//	        })
//	        Result(func() {
//	            Attribute("sum", Int)
//	        })
//	        Tool("add", "Add two numbers")
//	    })
//	})
func MCP(name, version string, opts ...func(*exprmcp.MCPExpr)) {
	svc, ok := eval.Current().(*goaexpr.ServiceExpr)
	if !ok {
		incompatibleDSL("MCP")
		return
	}
	m := &exprmcp.MCPExpr{Service: svc, Name: name, Version: version, Description: svc.Description, Capabilities: &exprmcp.CapabilitiesExpr{}}
	for _, o := range opts {
		if o != nil {
			o(m)
		}
	}
	if r := exprmcp.Root; r != nil {
		r.RegisterMCP(svc, m)
	}
}

// ToolSearchExactMatchNarrow suppresses weaker matches when a query exactly
// matches a generated tool name or title.
const ToolSearchExactMatchNarrow = exprmcp.ToolSearchExactMatchNarrow

// ToolSearchExactMatchBoost ranks exact matches highly but keeps weaker
// matches eligible.
const ToolSearchExactMatchBoost = exprmcp.ToolSearchExactMatchBoost

// ToolSearchExactMatchOff disables exact-match special handling.
const ToolSearchExactMatchOff = exprmcp.ToolSearchExactMatchOff

// IconThemeLight marks an icon as designed for light backgrounds.
const IconThemeLight = exprmcp.IconThemeLight

// IconThemeDark marks an icon as designed for dark backgrounds.
const IconThemeDark = exprmcp.IconThemeDark

// ToolSearchOption customizes generated progressive tool discovery search.
type ToolSearchOption func(*exprmcp.ToolSearchExpr)

// ToolSearchWeightOption customizes generated progressive discovery ranking
// weights.
type ToolSearchWeightOption func(*exprmcp.ToolSearchWeightsExpr)

// IconOption customizes one MCP icon metadata entry.
type IconOption func(*exprmcp.IconExpr)

// Icon builds MCP icon metadata for implementations, tools, resources, and prompts.
func Icon(src string, opts ...IconOption) *exprmcp.IconExpr {
	icon := &exprmcp.IconExpr{Source: strings.TrimSpace(src)}
	for _, opt := range opts {
		if opt != nil {
			opt(icon)
		}
	}
	return icon
}

// IconMIMEType sets the icon MIME type.
func IconMIMEType(mimeType string) IconOption {
	return func(icon *exprmcp.IconExpr) {
		icon.MIMEType = strings.TrimSpace(mimeType)
	}
}

// IconSizes sets the supported icon sizes.
func IconSizes(sizes ...string) IconOption {
	return func(icon *exprmcp.IconExpr) {
		icon.Sizes = append([]string(nil), sizes...)
	}
}

// IconTheme sets the icon theme preference.
func IconTheme(theme string) IconOption {
	return func(icon *exprmcp.IconExpr) {
		icon.Theme = strings.TrimSpace(theme)
	}
}

// WebsiteURL exposes the server implementation website URL.
func WebsiteURL(rawURL string) func(*exprmcp.MCPExpr) {
	return func(m *exprmcp.MCPExpr) {
		m.WebsiteURL = strings.TrimSpace(rawURL)
	}
}

// ToolSearch configures generated progressive discovery ranking defaults for
// an MCP server. Runtime adapter options can still override operational fields.
func ToolSearch(opts ...ToolSearchOption) func(*exprmcp.MCPExpr) {
	return func(m *exprmcp.MCPExpr) {
		search := &exprmcp.ToolSearchExpr{}
		for _, opt := range opts {
			if opt != nil {
				opt(search)
			}
		}
		m.ToolSearch = search
	}
}

// ToolSearchMaxResults sets the generated default search result cap.
func ToolSearchMaxResults(n int) ToolSearchOption {
	return func(search *exprmcp.ToolSearchExpr) {
		search.DefaultMaxResults = n
	}
}

// ToolSearchMinScore sets the generated minimum ranking score.
func ToolSearchMinScore(score int) ToolSearchOption {
	return func(search *exprmcp.ToolSearchExpr) {
		search.MinScore = score
	}
}

// ToolSearchExactMatch sets exact-name/title behavior. Use
// ToolSearchExactMatchNarrow, ToolSearchExactMatchBoost, or
// ToolSearchExactMatchOff.
func ToolSearchExactMatch(mode string) ToolSearchOption {
	return func(search *exprmcp.ToolSearchExpr) {
		search.ExactMatchMode = strings.TrimSpace(mode)
	}
}

// ToolSearchFuzzyNameMatching toggles fuzzy matching for tool names and titles.
func ToolSearchFuzzyNameMatching(enabled bool) ToolSearchOption {
	return func(search *exprmcp.ToolSearchExpr) {
		search.FuzzyNameMatching = boolPtr(enabled)
	}
}

// ToolSearchBroadFallback toggles weaker metadata, description, parameter, and
// schema matching when no strong name/title match exists.
func ToolSearchBroadFallback(enabled bool) ToolSearchOption {
	return func(search *exprmcp.ToolSearchExpr) {
		search.BroadFallback = boolPtr(enabled)
	}
}

// ToolSearchWeights configures generated ranking weights.
func ToolSearchWeights(opts ...ToolSearchWeightOption) ToolSearchOption {
	return func(search *exprmcp.ToolSearchExpr) {
		for _, opt := range opts {
			if opt != nil {
				opt(&search.Weights)
			}
		}
	}
}

// ToolSearchNameWeight sets the tool-name ranking weight.
func ToolSearchNameWeight(weight int) ToolSearchWeightOption {
	return func(weights *exprmcp.ToolSearchWeightsExpr) {
		weights.Name = intPtr(weight)
	}
}

// ToolSearchTitleWeight sets the tool-title ranking weight.
func ToolSearchTitleWeight(weight int) ToolSearchWeightOption {
	return func(weights *exprmcp.ToolSearchWeightsExpr) {
		weights.Title = intPtr(weight)
	}
}

// ToolSearchMetadataWeight sets the category/tag/keyword ranking weight.
func ToolSearchMetadataWeight(weight int) ToolSearchWeightOption {
	return func(weights *exprmcp.ToolSearchWeightsExpr) {
		weights.Metadata = intPtr(weight)
	}
}

// ToolSearchDescriptionWeight sets the description ranking weight.
func ToolSearchDescriptionWeight(weight int) ToolSearchWeightOption {
	return func(weights *exprmcp.ToolSearchWeightsExpr) {
		weights.Description = intPtr(weight)
	}
}

// ToolSearchParameterWeight sets the parameter/schema ranking weight.
func ToolSearchParameterWeight(weight int) ToolSearchWeightOption {
	return func(weights *exprmcp.ToolSearchWeightsExpr) {
		weights.Parameters = intPtr(weight)
	}
}

// ToolSearchFuzzyNameWeight sets the fuzzy name/title ranking weight.
func ToolSearchFuzzyNameWeight(weight int) ToolSearchWeightOption {
	return func(weights *exprmcp.ToolSearchWeightsExpr) {
		weights.FuzzyName = intPtr(weight)
	}
}

// OAuthOption configures one aspect of the MCP OAuth protected-resource
// configuration.
type OAuthOption func(*exprmcp.OAuthExpr)

// OAuth declares that the MCP server is an OAuth 2.0 protected resource.
// Pass AuthorizationServer, Scope, ResourceIdentifier, BearerMethodsSupported,
// and ResourceDocumentationURL options to populate the Protected Resource
// Metadata document (RFC 9728) and the WWW-Authenticate challenge.
//
// Example:
//
//	MCP("server", "1.0.0",
//	    OAuth(
//	        AuthorizationServer("https://auth.example.com"),
//	        OAuthScope("read", "Read tool results"),
//	        ResourceIdentifier("https://api.example.com/mcp"),
//	    ),
//	)
func OAuth(opts ...OAuthOption) func(*exprmcp.MCPExpr) {
	return func(m *exprmcp.MCPExpr) {
		o := &exprmcp.OAuthExpr{}
		for _, opt := range opts {
			if opt != nil {
				opt(o)
			}
		}
		m.OAuth = o
	}
}

// AuthorizationServer appends one OAuth 2.0 authorization server URL to the
// PRM document. The URL must use HTTPS and must not contain a query or fragment.
// Call multiple times to advertise more than one authorization server.
func AuthorizationServer(rawURL string) OAuthOption {
	return func(o *exprmcp.OAuthExpr) {
		server := strings.TrimSpace(rawURL)
		if server == "" {
			eval.ReportError("AuthorizationServer URL cannot be empty")
			return
		}
		o.AuthorizationServers = append(o.AuthorizationServers, server)
	}
}

// OAuthScope documents one OAuth 2.0 scope exposed by the protected
// resource. The name is "OAuthScope" rather than "Scope" to avoid
// colliding with Loom's core DSL Scope when both DSLs are
// dot-imported in a design file.
func OAuthScope(name, description string) OAuthOption {
	return func(o *exprmcp.OAuthExpr) {
		o.Scopes = append(o.Scopes, &exprmcp.ScopeExpr{
			Name:        strings.TrimSpace(name),
			Description: strings.TrimSpace(description),
		})
	}
}

// ResourceIdentifier pins the canonical resource URI emitted as the
// "resource" field in the Protected Resource Metadata document. When
// omitted, the generated handler derives the value from the incoming
// request URL, honoring X-Forwarded-* and Forwarded headers. Declaring
// ResourceIdentifier is the recommended production posture.
func ResourceIdentifier(url string) OAuthOption {
	return func(o *exprmcp.OAuthExpr) {
		o.ResourceIdentifier = strings.TrimSpace(url)
	}
}

// BearerMethodsSupported enumerates the OAuth 2.0 bearer token methods
// (header, body, query) the server accepts. Defaults to ["header"] at
// generation time when empty.
func BearerMethodsSupported(methods ...string) OAuthOption {
	return func(o *exprmcp.OAuthExpr) {
		for _, m := range methods {
			trimmed := strings.TrimSpace(m)
			if trimmed == "" {
				continue
			}
			o.BearerMethodsSupported = append(o.BearerMethodsSupported, trimmed)
		}
	}
}

// ResourceDocumentationURL surfaces as resource_documentation in the PRM
// document.
func ResourceDocumentationURL(url string) OAuthOption {
	return func(o *exprmcp.OAuthExpr) {
		o.ResourceDocumentationURL = strings.TrimSpace(url)
	}
}

// TrustProxyHeaders opts the generated server into honoring
// X-Forwarded-Proto, X-Forwarded-Host, and RFC 7239 Forwarded headers
// when deriving the canonical resource URL and the WWW-Authenticate
// challenge origin. Default (without this option) is not to trust
// forwarded headers at all.
//
// Only enable this when every request reaches the server through a
// reverse proxy the operator fully controls and that strips these
// headers from direct-client requests. A server reachable directly by
// clients must NOT trust forwarded headers — an attacker would otherwise
// control the PRM `resource` field advertised to clients.
//
// For most production deployments, pinning ResourceIdentifier(...) is
// preferred: a declared identifier bypasses forwarded-header derivation
// entirely and is the spec's recommended posture.
func TrustProxyHeaders() OAuthOption {
	return func(o *exprmcp.OAuthExpr) {
		o.TrustProxyHeaders = true
	}
}

// ServerIcons attaches implementation icons to the MCP server metadata.
func ServerIcons(icons ...*exprmcp.IconExpr) func(*exprmcp.MCPExpr) {
	return func(m *exprmcp.MCPExpr) {
		m.Icons = cloneIcons(icons)
	}
}

// ToolIcons attaches icon metadata to an MCP tool.
func ToolIcons(icons ...*exprmcp.IconExpr) func(*exprmcp.ToolExpr) {
	return func(tool *exprmcp.ToolExpr) {
		tool.Icons = cloneIcons(icons)
	}
}

// ToolTitle sets the MCP tool display title.
func ToolTitle(title string) func(*exprmcp.ToolExpr) {
	return func(tool *exprmcp.ToolExpr) {
		tool.Title = strings.TrimSpace(title)
	}
}

// ToolDiscoveryCategory sets the MCP tool progressive discovery category.
func ToolDiscoveryCategory(category string) func(*exprmcp.ToolExpr) {
	return func(tool *exprmcp.ToolExpr) {
		tool.DiscoveryCategory = strings.TrimSpace(category)
	}
}

// ToolDiscoveryTags sets the MCP tool progressive discovery tags.
func ToolDiscoveryTags(tags ...string) func(*exprmcp.ToolExpr) {
	return func(tool *exprmcp.ToolExpr) {
		tool.DiscoveryTags = cleanStrings(tags)
	}
}

// ToolDiscoveryKeywords sets the MCP tool progressive discovery keywords.
func ToolDiscoveryKeywords(keywords ...string) func(*exprmcp.ToolExpr) {
	return func(tool *exprmcp.ToolExpr) {
		tool.DiscoveryKeywords = cleanStrings(keywords)
	}
}

// ToolDiscoveryCallTemplateArg adds an exemplar argument to progressive
// discovery call_tool templates. The argument remains optional unless the
// payload schema itself marks it required.
func ToolDiscoveryCallTemplateArg(name string, value any) func(*exprmcp.ToolExpr) {
	return func(tool *exprmcp.ToolExpr) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if tool.DiscoveryCallTemplateArgs == nil {
			tool.DiscoveryCallTemplateArgs = make(map[string]any)
		}
		tool.DiscoveryCallTemplateArgs[name] = value
	}
}

// ResourceIcons attaches icon metadata to an MCP resource.
func ResourceIcons(icons ...*exprmcp.IconExpr) func(*exprmcp.ResourceExpr) {
	return func(resource *exprmcp.ResourceExpr) {
		resource.Icons = cloneIcons(icons)
	}
}

// PromptIcons attaches icon metadata to a static MCP prompt.
func PromptIcons(icons ...*exprmcp.IconExpr) func(*exprmcp.PromptExpr) {
	return func(prompt *exprmcp.PromptExpr) {
		prompt.Icons = cloneIcons(icons)
	}
}

// RuntimePromptOption customizes the runtime prompt spec generated for a static
// MCP prompt.
type RuntimePromptOption func(*exprmcp.RuntimePromptExpr)

// RuntimePrompt declares that a static MCP prompt should also be registered as a
// runtime prompt spec. The static prompt must contain exactly one message so the
// generated prompt spec has one unambiguous template body.
func RuntimePrompt(agentID, role string, opts ...RuntimePromptOption) func(*exprmcp.PromptExpr) {
	return func(prompt *exprmcp.PromptExpr) {
		runtime := &exprmcp.RuntimePromptExpr{
			AgentID: strings.TrimSpace(agentID),
			Role:    strings.TrimSpace(role),
		}
		for _, opt := range opts {
			if opt != nil {
				opt(runtime)
			}
		}
		prompt.Runtime = runtime
	}
}

// RuntimePromptVersion sets the baseline version for a generated runtime
// prompt spec. When omitted, the runtime prompt registry derives a deterministic
// version from the template.
func RuntimePromptVersion(version string) RuntimePromptOption {
	return func(runtime *exprmcp.RuntimePromptExpr) {
		runtime.Version = strings.TrimSpace(version)
	}
}

// DynamicPromptIcons attaches icon metadata to a dynamic MCP prompt.
func DynamicPromptIcons(icons ...*exprmcp.IconExpr) func(*exprmcp.DynamicPromptExpr) {
	return func(prompt *exprmcp.DynamicPromptExpr) {
		prompt.Icons = cloneIcons(icons)
	}
}

// Resource marks the current method as an MCP resource provider. The method's
// result becomes the resource content returned when clients read the resource.
//
// Resource must appear in a Method expression within a service that has MCP enabled.
//
// Resource takes three arguments:
//   - name: the resource name (used in MCP resource list)
//   - uri: the resource URI (e.g., "file:///docs/readme.md")
//   - mimeType: the content MIME type (e.g., "text/plain", "application/json")
//
// Example:
//
//	Method("readme", func() {
//	    Result(String)
//	    Resource("readme", "file:///docs/README.md", "text/markdown")
//	})
func Resource(name, uri, mimeType string, opts ...func(*exprmcp.ResourceExpr)) {
	parent := eval.Current()
	method, isMethod := parent.(*goaexpr.MethodExpr)
	if !isMethod {
		incompatibleDSL("Resource")
		return
	}
	svc := method.Service
	var mcp *exprmcp.MCPExpr
	if r := exprmcp.Root; r != nil {
		mcp = r.GetMCP(svc)
	}
	if mcp == nil {
		mcpRequiredDSL("Resource", svc)
		return
	}
	resource := &exprmcp.ResourceExpr{Name: name, Description: method.Description, URI: uri, MimeType: mimeType, Method: method}
	for _, opt := range opts {
		if opt != nil {
			opt(resource)
		}
	}
	mcp.Resources = append(mcp.Resources, resource)
}

// WatchableResource marks the current method as an MCP resource that supports
// subscriptions. Clients can subscribe to receive notifications when the resource
// content changes.
//
// WatchableResource must appear in a Method expression within a service that has
// MCP enabled.
//
// WatchableResource takes three arguments:
//   - name: the resource name (used in MCP resource list)
//   - uri: the resource URI (e.g., "file:///logs/app.log")
//   - mimeType: the content MIME type (e.g., "text/plain")
//
// Example:
//
//	Method("system_status", func() {
//	    Result(func() {
//	        Attribute("status", String)
//	        Attribute("uptime", Int)
//	    })
//	    WatchableResource("status", "status://system", "application/json")
//	})
func WatchableResource(name, uri, mimeType string, opts ...func(*exprmcp.ResourceExpr)) {
	parent := eval.Current()
	method, isMethod := parent.(*goaexpr.MethodExpr)
	if !isMethod {
		incompatibleDSL("WatchableResource")
		return
	}
	svc := method.Service
	var mcp *exprmcp.MCPExpr
	if r := exprmcp.Root; r != nil {
		mcp = r.GetMCP(svc)
	}
	if mcp == nil {
		mcpRequiredDSL("WatchableResource", svc)
		return
	}
	resource := &exprmcp.ResourceExpr{Name: name, Description: method.Description, URI: uri, MimeType: mimeType, Method: method, Watchable: true}
	for _, opt := range opts {
		if opt != nil {
			opt(resource)
		}
	}
	mcp.Resources = append(mcp.Resources, resource)
}

// SkillDirectory exposes agent skill directories as MCP skill:// resources.
//
// The root directory is scanned at runtime. Each child directory containing a
// SKILL.md file becomes a skill with resources for SKILL.md, _manifest, and any
// supporting files named by the manifest.
func SkillDirectory(root string) func(*exprmcp.MCPExpr) {
	dir := &exprmcp.SkillDirectoryExpr{Root: strings.TrimSpace(root)}
	var mcp *exprmcp.MCPExpr
	var svc *goaexpr.ServiceExpr
	if current, ok := eval.Current().(*goaexpr.ServiceExpr); ok {
		svc = current
		if r := exprmcp.Root; r != nil {
			mcp = r.GetMCP(svc)
		}
	}
	if mcp != nil {
		mcp.SkillDirectories = append(mcp.SkillDirectories, dir)
	} else if svc != nil && exprmcp.Root != nil {
		exprmcp.Root.DeferSkillDirectory(svc, dir)
	}
	return func(m *exprmcp.MCPExpr) {
		if svc != nil && exprmcp.Root != nil {
			exprmcp.Root.ConsumeDeferredSkillDirectory(svc, dir)
		}
		m.SkillDirectories = append(m.SkillDirectories, dir)
	}
}

// StaticPrompt adds a static prompt template to the MCP server. Static prompts
// provide pre-defined message sequences that clients can use without parameters.
//
// The service must also declare MCP; declaration order does not matter.
//
// StaticPrompt takes a name, description, and a list of role-content pairs:
//   - name: the prompt identifier
//   - description: human-readable prompt description
//   - messages: alternating role and content strings (e.g., "user", "text", "assistant", "text")
//
// Example:
//
//	Service("assistant", func() {
//	    MCP("assistant", "1.0")
//	    StaticPrompt("greeting", "Friendly greeting",
//	        "user", "Hello!",
//	        "assistant", "How can I help?")
//	})
func StaticPrompt(name, description string, args ...any) {
	svc, isService := eval.Current().(*goaexpr.ServiceExpr)
	if !isService {
		incompatibleDSL("StaticPrompt")
		return
	}
	prompt := &exprmcp.PromptExpr{Name: name, Description: description, Messages: make([]*exprmcp.MessageExpr, 0)}
	var messages []string
	for _, arg := range args {
		switch actual := arg.(type) {
		case string:
			messages = append(messages, actual)
		case func(*exprmcp.PromptExpr):
			actual(prompt)
		default:
			eval.InvalidArgError("string or MCP prompt option", arg)
			return
		}
	}
	if len(messages)%2 != 0 {
		eval.ReportError("StaticPrompt %q requires role/content string pairs; missing content for role %q", name, messages[len(messages)-1])
		return
	}
	for i := 0; i < len(messages); i += 2 {
		prompt.Messages = append(prompt.Messages, &exprmcp.MessageExpr{Role: messages[i], Content: messages[i+1]})
	}
	if r := exprmcp.Root; r != nil {
		if mcp := r.GetMCP(svc); mcp != nil {
			mcp.Prompts = append(mcp.Prompts, prompt)
		} else {
			r.DeferStaticPrompt(svc, prompt)
		}
		return
	}
	mcpRequiredDSL("StaticPrompt", svc)
}

// DynamicPrompt marks the current method as a dynamic prompt generator. The
// method's payload defines parameters that customize the generated prompt, and
// the result contains the generated message sequence.
//
// DynamicPrompt must appear in a Method expression within a service that has MCP enabled.
//
// DynamicPrompt takes two arguments:
//   - name: the non-empty prompt identifier
//   - description: human-readable prompt description
//
// Each icon must have a non-empty source and a valid theme.
//
// Example:
//
//	Method("code_review", func() {
//	    Payload(func() {
//	        Attribute("language", String)
//	        Attribute("code", String)
//	    })
//	    Result(ArrayOf(Message))
//	    DynamicPrompt("code_review", "Generate code review prompt")
//	})
func DynamicPrompt(name, description string, opts ...func(*exprmcp.DynamicPromptExpr)) {
	parent := eval.Current()
	method, isMethod := parent.(*goaexpr.MethodExpr)
	if !isMethod {
		incompatibleDSL("DynamicPrompt")
		return
	}
	svc := method.Service
	if exprmcp.Root == nil || exprmcp.Root.GetMCP(svc) == nil {
		mcpRequiredDSL("DynamicPrompt", svc)
		return
	}
	prompt := &exprmcp.DynamicPromptExpr{Name: name, Description: description, Method: method}
	for _, opt := range opts {
		if opt != nil {
			opt(prompt)
		}
	}
	exprmcp.Root.RegisterDynamicPrompt(svc, prompt)
}

func cloneIcons(icons []*exprmcp.IconExpr) []*exprmcp.IconExpr {
	if len(icons) == 0 {
		return nil
	}
	out := make([]*exprmcp.IconExpr, 0, len(icons))
	for _, icon := range icons {
		if icon == nil {
			continue
		}
		copyIcon := *icon
		copyIcon.Sizes = append([]string(nil), icon.Sizes...)
		out = append(out, &copyIcon)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}
