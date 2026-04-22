// Package mcp defines the expression types used to represent MCP server
// configuration during Goa design evaluation. These types are populated during
// DSL execution and form the schema used for MCP protocol code generation.
package mcp

import (
	"errors"
	"net/url"

	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
)

var (
	errAbsoluteURLRequired        = errors.New("must be an absolute URL with scheme and host")
	errResourceIdentifierFragment = errors.New("must not contain a fragment")
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
		// ProtocolVersion is the MCP protocol version this server
		// implements.
		ProtocolVersion string
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
		// Prompts is the collection of static prompt expressions
		// exposed by this server.
		Prompts []*PromptExpr
		// Notifications is the collection of notification expressions
		// this server can send.
		Notifications []*NotificationExpr
		// Subscriptions is the collection of resource subscription
		// expressions this server supports.
		Subscriptions []*SubscriptionExpr
		// SubscriptionMonitors is the collection of subscription
		// monitor expressions for SSE.
		SubscriptionMonitors []*SubscriptionMonitorExpr
		// OAuth is the optional OAuth 2.0 protected-resource configuration
		// that drives Protected Resource Metadata emission and the
		// WWW-Authenticate challenge. When nil, the server does not
		// advertise OAuth discovery.
		OAuth *OAuthExpr
		// Service is the Goa service expression this MCP server is
		// bound to.
		Service *expr.ServiceExpr
	}

	// OAuthExpr describes the OAuth 2.0 protected-resource configuration
	// exposed by an MCP server. It backs the Protected Resource Metadata
	// document (RFC 9728) served at .well-known/oauth-protected-resource
	// and the WWW-Authenticate challenge emitted for unauthenticated
	// requests.
	OAuthExpr struct {
		// AuthorizationServers lists the OAuth 2.0 authorization servers
		// that can issue tokens for this resource. Required; at least
		// one entry.
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
		// EnableLogging indicates whether the server supports logging.
		EnableLogging bool
		// EnableProgress indicates whether the server supports progress
		// notifications.
		EnableProgress bool
		// EnableCancellation indicates whether the server supports
		// request cancellation.
		EnableCancellation bool
		// EnableNotifications indicates whether the server can send
		// notifications.
		EnableNotifications bool
		// EnableCompletion indicates whether the server supports
		// completion suggestions.
		EnableCompletion bool
		// EnablePagination indicates whether the server supports
		// paginated responses.
		EnablePagination bool
		// EnableSubscriptions indicates whether the server supports
		// resource subscriptions.
		EnableSubscriptions bool
	}

	// ToolExpr defines an MCP tool that the server exposes for invocation.
	ToolExpr struct {
		eval.Expression

		// Name is the unique identifier for this tool.
		Name string
		// Description provides a human-readable explanation of what the
		// tool does.
		Description string
		// Method is the Goa service method that implements this tool.
		Method *expr.MethodExpr
		// InputSchema defines the parameter schema for this tool.
		InputSchema *expr.AttributeExpr
		// Icons is the optional icon metadata exposed for this tool.
		Icons []*IconExpr
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
		// Icons is the optional icon metadata exposed for this prompt.
		Icons []*IconExpr
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

	// NotificationExpr defines a notification that the server can send to
	// clients.
	NotificationExpr struct {
		eval.Expression

		// Name is the unique identifier for this notification type.
		Name string
		// Description provides a human-readable explanation of the
		// notification.
		Description string
		// Method is the Goa service method that sends this notification.
		Method *expr.MethodExpr
	}

	// SubscriptionExpr defines a subscription to resource change events.
	SubscriptionExpr struct {
		eval.Expression

		// ResourceName is the name of the resource being subscribed to.
		ResourceName string
		// Method is the Goa service method that handles this subscription.
		Method *expr.MethodExpr
	}

	// SubscriptionMonitorExpr defines a subscription monitor for SSE-based
	// subscriptions.
	SubscriptionMonitorExpr struct {
		eval.Expression

		// Name is the unique identifier for this monitor.
		Name string
		// Method is the Goa service method that implements the monitor.
		Method *expr.MethodExpr
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
)

// Finalize finalizes the MCP expression
func (m *MCPExpr) Finalize() {
	if m.Transport == "" {
		m.Transport = "jsonrpc"
	}
	if m.Capabilities == nil {
		m.Capabilities = &CapabilitiesExpr{}
	}
	if len(m.Tools) > 0 {
		m.Capabilities.EnableTools = true
	}
	if len(m.Resources) > 0 {
		m.Capabilities.EnableResources = true
	}
	if len(m.Prompts) > 0 {
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
	mergeChildErrors(verr, m.Tools, toolValidator)
	mergeChildErrors(verr, m.Resources, resourceValidator)
	mergeChildErrors(verr, m.Prompts, promptValidator)
	if m.OAuth != nil {
		mergeValidationError(verr, m.OAuth.Validate())
	}
	if len(verr.Errors) > 0 {
		return verr
	}
	return nil
}

func iconValidator(icon *IconExpr) error      { return icon.Validate() }
func toolValidator(t *ToolExpr) error         { return t.Validate() }
func resourceValidator(r *ResourceExpr) error { return r.Validate() }
func promptValidator(p *PromptExpr) error     { return p.Validate() }

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
		verr.Add(nil, "OAuth requires at least one AuthorizationServer")
	}
	seenScope := make(map[string]struct{}, len(o.Scopes))
	for _, scope := range o.Scopes {
		if scope == nil {
			continue
		}
		if scope.Name == "" {
			verr.Add(nil, "OAuth scope name is required")
			continue
		}
		if _, dup := seenScope[scope.Name]; dup {
			verr.Add(nil, "OAuth scope %q declared more than once", scope.Name)
			continue
		}
		seenScope[scope.Name] = struct{}{}
	}
	for _, method := range o.BearerMethodsSupported {
		switch method {
		case "header", "body", "query":
		default:
			verr.Add(nil, "OAuth BearerMethodsSupported must be header, body, or query; got %q", method)
		}
	}
	if id := o.ResourceIdentifier; id != "" {
		if err := validateResourceIdentifier(id); err != nil {
			verr.Add(nil, "OAuth ResourceIdentifier invalid: %s", err.Error())
		}
	}
	if len(verr.Errors) > 0 {
		return verr
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
		return errResourceIdentifierFragment
	}
	return nil
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
	for _, icon := range t.Icons {
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
func (p *PromptExpr) EvalName() string {
	return "MCP prompt " + p.Name
}

// EvalName returns the name used for evaluation.
func (m *MessageExpr) EvalName() string {
	return "MCP message"
}

// EvalName returns the name used for evaluation.
func (d *DynamicPromptExpr) EvalName() string {
	return "MCP dynamic prompt " + d.Name
}

// EvalName returns the name used for evaluation.
func (n *NotificationExpr) EvalName() string {
	return "MCP notification " + n.Name
}

// EvalName returns the name used for evaluation.
func (s *SubscriptionExpr) EvalName() string {
	return "MCP subscription for resource " + s.ResourceName
}

// EvalName returns the name used for evaluation.
func (s *SubscriptionMonitorExpr) EvalName() string {
	return "MCP subscription monitor " + s.Name
}
