package dsl

import (
	"strings"

	_ "github.com/CaliLuke/loom-mcp/codegen/mcp" // Registers the MCP codegen plugin with Goa
	exprmcp "github.com/CaliLuke/loom-mcp/expr/mcp"
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
// MCP takes two required arguments and an optional list of configuration
// functions:
//   - name: the MCP server name (used in MCP handshake)
//   - version: the server version string
//   - opts: optional configuration functions (e.g., ProtocolVersion)
//
// Example:
//
//	Service("calculator", func() {
//	    MCP("calc", "1.0.0", ProtocolVersion("2025-06-18"))
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
		eval.IncompatibleDSL()
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

// ProtocolVersion configures the MCP protocol version supported by the server.
// It returns a configuration function for use with MCP.
//
// ProtocolVersion takes a single argument which is the protocol version string.
//
// Example:
//
//	Service("calculator", func() {
//	    MCP("calc", "1.0.0", ProtocolVersion("2025-06-18"))
//	})
func ProtocolVersion(version string) func(*exprmcp.MCPExpr) {
	return func(m *exprmcp.MCPExpr) { m.ProtocolVersion = version }
}

// IconThemeLight marks an icon as designed for light backgrounds.
const IconThemeLight = exprmcp.IconThemeLight

// IconThemeDark marks an icon as designed for dark backgrounds.
const IconThemeDark = exprmcp.IconThemeDark

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
		eval.IncompatibleDSL()
		return
	}
	svc := method.Service
	var mcp *exprmcp.MCPExpr
	if r := exprmcp.Root; r != nil {
		mcp = r.GetMCP(svc)
	}
	if mcp == nil {
		eval.IncompatibleDSL()
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
		eval.IncompatibleDSL()
		return
	}
	svc := method.Service
	var mcp *exprmcp.MCPExpr
	if r := exprmcp.Root; r != nil {
		mcp = r.GetMCP(svc)
	}
	if mcp == nil {
		eval.IncompatibleDSL()
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

// StaticPrompt adds a static prompt template to the MCP server. Static prompts
// provide pre-defined message sequences that clients can use without parameters.
//
// StaticPrompt must appear in a Service expression with MCP enabled.
//
// StaticPrompt takes a name, description, and a list of role-content pairs:
//   - name: the prompt identifier
//   - description: human-readable prompt description
//   - messages: alternating role and content strings (e.g., "user", "text", "system", "text")
//
// Example:
//
//	Service("assistant", func() {
//	    MCP("assistant", "1.0")
//	    StaticPrompt("greeting", "Friendly greeting",
//	        "system", "You are a helpful assistant",
//	        "user", "Hello!")
//	})
func StaticPrompt(name, description string, args ...any) {
	var mcp *exprmcp.MCPExpr
	if svc, ok := eval.Current().(*goaexpr.ServiceExpr); ok {
		if r := exprmcp.Root; r != nil {
			mcp = r.GetMCP(svc)
		}
	}
	if mcp == nil {
		eval.IncompatibleDSL()
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
	for i := 0; i < len(messages); i += 2 {
		if i+1 < len(messages) {
			prompt.Messages = append(prompt.Messages, &exprmcp.MessageExpr{Role: messages[i], Content: messages[i+1]})
		}
	}
	mcp.Prompts = append(mcp.Prompts, prompt)
}

// DynamicPrompt marks the current method as a dynamic prompt generator. The
// method's payload defines parameters that customize the generated prompt, and
// the result contains the generated message sequence.
//
// DynamicPrompt must appear in a Method expression within a service that has MCP enabled.
//
// DynamicPrompt takes two arguments:
//   - name: the prompt identifier
//   - description: human-readable prompt description
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
		eval.IncompatibleDSL()
		return
	}
	svc := method.Service
	prompt := &exprmcp.DynamicPromptExpr{Name: name, Description: description, Method: method}
	for _, opt := range opts {
		if opt != nil {
			opt(prompt)
		}
	}
	if r := exprmcp.Root; r != nil {
		r.RegisterDynamicPrompt(svc, prompt)
	}
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

// Notification marks the current method as an MCP notification sender. The
// method's payload defines the notification message structure.
//
// Notification must appear in a Method expression within a service that has MCP enabled.
//
// Notification takes two arguments:
//   - name: the notification identifier
//   - description: human-readable notification description
//
// Example:
//
//	Method("progress_update", func() {
//	    Payload(func() {
//	        Attribute("task_id", String)
//	        Attribute("progress", Int)
//	    })
//	    Notification("progress", "Task progress notification")
//	})
func Notification(name, description string) {
	parent := eval.Current()
	method, isMethod := parent.(*goaexpr.MethodExpr)
	if !isMethod {
		eval.IncompatibleDSL()
		return
	}
	svc := method.Service
	var mcp *exprmcp.MCPExpr
	if r := exprmcp.Root; r != nil {
		mcp = r.GetMCP(svc)
	}
	if mcp == nil {
		eval.IncompatibleDSL()
		return
	}
	notif := &exprmcp.NotificationExpr{Name: name, Description: description, Method: method}
	mcp.Notifications = append(mcp.Notifications, notif)
}

// Subscription marks the current method as a subscription handler for a
// watchable resource. The method is invoked when clients subscribe to the
// resource identified by resourceName.
//
// Subscription must appear in a Method expression within a service that has MCP enabled.
//
// Subscription takes a single argument which is the resource name to subscribe to.
// The resource name must match a WatchableResource declaration.
//
// Example:
//
//	Method("subscribe_status", func() {
//	    Payload(func() {
//	        Attribute("uri", String)
//	    })
//	    Result(String)
//	    Subscription("status")
//	})
func Subscription(resourceName string) {
	parent := eval.Current()
	method, isMethod := parent.(*goaexpr.MethodExpr)
	if !isMethod {
		eval.IncompatibleDSL()
		return
	}
	svc := method.Service
	var mcp *exprmcp.MCPExpr
	if r := exprmcp.Root; r != nil {
		mcp = r.GetMCP(svc)
	}
	if mcp == nil {
		eval.IncompatibleDSL()
		return
	}
	sub := &exprmcp.SubscriptionExpr{ResourceName: resourceName, Method: method}
	mcp.Subscriptions = append(mcp.Subscriptions, sub)
}

// SubscriptionMonitor marks the current method as a server-sent events (SSE)
// monitor for subscription updates. The method streams subscription change events
// to connected clients.
//
// SubscriptionMonitor must appear in a Method expression within a service that has MCP enabled.
//
// SubscriptionMonitor takes a single argument which is the monitor name.
//
// Example:
//
//	Method("watch_subscriptions", func() {
//	    StreamingResult(func() {
//	        Attribute("resource", String)
//	        Attribute("event", String)
//	    })
//	    SubscriptionMonitor("subscriptions")
//	})
func SubscriptionMonitor(name string) {
	parent := eval.Current()
	method, isMethod := parent.(*goaexpr.MethodExpr)
	if !isMethod {
		eval.IncompatibleDSL()
		return
	}
	svc := method.Service
	var mcp *exprmcp.MCPExpr
	if r := exprmcp.Root; r != nil {
		mcp = r.GetMCP(svc)
	}
	if mcp == nil {
		eval.IncompatibleDSL()
		return
	}
	monitor := &exprmcp.SubscriptionMonitorExpr{Name: name, Method: method}
	mcp.SubscriptionMonitors = append(mcp.SubscriptionMonitors, monitor)
}
