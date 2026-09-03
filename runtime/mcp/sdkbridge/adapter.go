package sdkbridge

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	mcpskills "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/skills"
	loom "github.com/CaliLuke/loom/pkg"
)

// ToolCallInterceptorInfo describes an MCP tools/call invocation.
type ToolCallInterceptorInfo interface {
	loom.InterceptorInfo
	Tool() string
	RawArguments() jsontext.Value
}

// TypedHandler preserves generated payload and result types across runtime orchestration.
type TypedHandler[P, R any] func(context.Context, P) (R, error)

// TypedInterceptor wraps a TypedHandler without erasing its payload or result types.
type TypedInterceptor[P, R any] func(context.Context, ToolCallInterceptorInfo, P, TypedHandler[P, R]) (R, error)

// NamedOperation describes one generated named prompt operation.
type NamedOperation[P, R any] struct {
	Name   string
	Handle TypedHandler[P, R]
}

// NamedDispatchConfig configures common named-operation orchestration.
type NamedDispatchConfig[P, R any] struct {
	Method         string
	Initialized    func(context.Context) bool
	Name           func(P) string
	Operations     []NamedOperation[P, R]
	Log            func(context.Context, string, any)
	MapError       func(error, string, string) error
	FailureCode    string
	FailureMessage string
	MissingName    string
	UnknownName    string
}

// ResourceOperation describes one generated resource URI and typed service closure.
type ResourceOperation[P, R any] struct {
	URI    string
	Handle func(context.Context, P, string) (R, error)
}

// ResourceQueryField describes one designed resource query parameter.
type ResourceQueryField = mcpruntime.QueryField

// ResourceURIMatcher matches one precompiled resource URI template and its designed query shape.
type ResourceURIMatcher struct {
	Pattern     *regexp.Regexp
	QueryFields map[string]ResourceQueryField
}

// Match reports whether uri matches the template and declares only designed query parameters.
func (matcher ResourceURIMatcher) Match(uri string) bool {
	if matcher.Pattern == nil || !matcher.Pattern.MatchString(uri) {
		return false
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range query {
		field, ok := matcher.QueryFields[key]
		if !ok || !field.Repeated && len(values) > 1 {
			return false
		}
	}
	return true
}

type ResourcePolicy struct {
	AllowedURIs       []string
	DeniedURIs        []string
	AllowedNames      []string
	DeniedNames       []string
	ResourceNameToURI map[string]string
}

// ResourceDispatchConfig configures common resource policy and dispatch orchestration.
type ResourceDispatchConfig[P, R any] struct {
	Initialized  func(context.Context) bool
	URI          func(P) string
	Policy       ResourcePolicy
	Resources    []ResourceOperation[P, R]
	SkillSources []mcpskills.Source
	SkillResult  func(*mcpskills.Content) R
	Log          func(context.Context, string, any)
	MapError     func(error, string, string) error
}

type toolCallInfo struct {
	service    string
	method     string
	tool       string
	rawPayload any
	rawArgs    jsontext.Value
}

type invalidClientInputError struct{ error }

// InvalidClientInput marks a generated codec or validation failure as safe client input feedback.
func InvalidClientInput(err error) error {
	if err == nil || mcpruntime.IsInvalidClientInput(err) {
		return err
	}
	return invalidClientInputError{error: err}
}

func (err invalidClientInputError) InvalidClientInput() {}
func (err invalidClientInputError) Unwrap() error       { return err.error }

// NewToolCallInfo constructs immutable metadata for generated tool interceptors.
func NewToolCallInfo(service, tool string, payload any, rawArguments jsontext.Value) ToolCallInterceptorInfo {
	return &toolCallInfo{
		service:    service,
		method:     "tools/call",
		tool:       tool,
		rawPayload: payload,
		rawArgs:    rawArguments,
	}
}

// WrapTypedHandler applies interceptors in declaration order, with the first outermost.
func WrapTypedHandler[P, R any](interceptors []TypedInterceptor[P, R], info ToolCallInterceptorInfo, next TypedHandler[P, R]) TypedHandler[P, R] {
	wrapped := next
	for i := len(interceptors) - 1; i >= 0; i-- {
		interceptor := interceptors[i]
		if interceptor == nil {
			continue
		}
		current := wrapped
		wrapped = func(ctx context.Context, payload P) (R, error) {
			return interceptor(ctx, info, payload, current)
		}
	}
	return wrapped
}

// DispatchNamed validates and dispatches a generated prompt descriptor.
func DispatchNamed[P, R any](ctx context.Context, payload P, config NamedDispatchConfig[P, R]) (R, error) {
	var zero R
	if config.Initialized != nil && !config.Initialized(ctx) {
		return zero, loom.PermanentError("invalid_params", "Not initialized")
	}
	name := ""
	if config.Name != nil {
		name = config.Name(payload)
	}
	if name == "" {
		return zero, loom.PermanentError("invalid_params", "%s", config.MissingName)
	}
	logDispatch(config.Log, ctx, "request", config.Method, "name", name)
	for _, operation := range config.Operations {
		if operation.Name != name {
			continue
		}
		if operation.Handle == nil {
			return zero, mapDispatchError(config.MapError, fmt.Errorf("MCP %s descriptor %q has no handler", config.Method, name), config.FailureCode, config.FailureMessage)
		}
		result, err := operation.Handle(ctx, payload)
		if err != nil {
			return zero, mapDispatchError(config.MapError, err, config.FailureCode, config.FailureMessage)
		}
		logDispatch(config.Log, ctx, "response", config.Method, "name", name)
		return result, nil
	}
	return zero, loom.PermanentError("invalid_params", config.UnknownName, name)
}

// DispatchResource applies resource policy and invokes one generated typed descriptor.
func DispatchResource[P, R any](ctx context.Context, payload P, config ResourceDispatchConfig[P, R]) (R, error) {
	var zero R
	if config.Initialized != nil && !config.Initialized(ctx) {
		return zero, loom.PermanentError("invalid_params", "Not initialized")
	}
	uri := ""
	if config.URI != nil {
		uri = config.URI(payload)
	}
	if uri == "" {
		return zero, loom.PermanentError("invalid_params", "Missing resource URI")
	}
	logDispatch(config.Log, ctx, "request", "resources/read", "uri", uri)
	baseURI := uri
	if index := strings.IndexByte(baseURI, '?'); index >= 0 {
		baseURI = baseURI[:index]
	}
	if strings.HasPrefix(baseURI, "skill://") && len(config.SkillSources) > 0 {
		return dispatchSkillResource(ctx, uri, baseURI, config)
	}
	if err := config.Policy.Authorize(ctx, uri, nil); err != nil {
		return zero, mapDispatchError(config.MapError, err, "invalid_params", "Resource URI is not allowed.")
	}
	for _, resource := range config.Resources {
		if resource.URI != baseURI {
			continue
		}
		if resource.Handle == nil {
			return zero, mapDispatchError(config.MapError, fmt.Errorf("MCP resource descriptor %q has no handler", baseURI), "internal_error", "Resource read failed.")
		}
		result, err := resource.Handle(ctx, payload, baseURI)
		if err != nil {
			return zero, mapDispatchError(config.MapError, err, "internal_error", "Resource read failed.")
		}
		logDispatch(config.Log, ctx, "response", "resources/read", "uri", baseURI)
		return result, nil
	}
	return zero, loom.PermanentError("resource_not_found", "Unknown resource: %s", uri)
}

// ResourceQueryJSON converts a resource URI query into an inferred JSON object.
func ResourceQueryJSON(uri string) ([]byte, error) {
	return ResourceQueryJSONTyped(uri, nil)
}

// ResourceQueryJSONTyped converts a resource URI query into JSON while
// preserving the string and repeated shapes declared by generated metadata.
func ResourceQueryJSONTyped(uri string, fields map[string]mcpruntime.QueryField) ([]byte, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid resource URI: %w", err)
	}
	query := parsed.Query()
	if len(query) == 0 {
		return []byte("{}"), nil
	}
	values := make(map[string][]string, len(query))
	for key, value := range query {
		values[key] = value
	}
	return json.Marshal(mcpruntime.CoerceQueryTyped(values, fields))
}

// Authorize verifies a resource URI against server and request-scoped policy.
func (policy ResourcePolicy) Authorize(ctx context.Context, uri string, extraNameToURI map[string]string) error {
	baseURI := uri
	if index := strings.IndexByte(baseURI, '?'); index >= 0 {
		baseURI = baseURI[:index]
	}
	var requestAllowedNames, requestDeniedNames []string
	if ctx != nil {
		requestAllowedNames = splitResourceNames(mcpruntime.AllowedResourceNamesFromContext(ctx))
		requestDeniedNames = splitResourceNames(mcpruntime.DeniedResourceNamesFromContext(ctx))
	}
	deniedNames := append([]string(nil), policy.DeniedNames...)
	deniedNames = append(deniedNames, requestDeniedNames...)

	serverNameAllowURIs := resolveResourceNames(policy.AllowedNames, extraNameToURI, policy.ResourceNameToURI)
	requestNameAllowURIs := resolveResourceNames(requestAllowedNames, extraNameToURI, policy.ResourceNameToURI)
	extraDenyURIs := resolveResourceNames(deniedNames, extraNameToURI, policy.ResourceNameToURI)
	for _, denied := range policy.DeniedURIs {
		if resourceURIMatchesPolicy(baseURI, denied) {
			return fmt.Errorf("resource URI denied: %s", uri)
		}
	}
	for _, denied := range extraDenyURIs {
		if resourceURIMatchesPolicy(baseURI, denied) {
			return fmt.Errorf("resource URI denied: %s", uri)
		}
	}
	serverAllowPolicies := make([]string, 0, len(policy.AllowedURIs)+len(serverNameAllowURIs))
	serverAllowPolicies = append(serverAllowPolicies, policy.AllowedURIs...)
	serverAllowPolicies = append(serverAllowPolicies, serverNameAllowURIs...)
	if !resourceURIAllowedByPolicies(baseURI, len(policy.AllowedURIs) > 0 || len(policy.AllowedNames) > 0, serverAllowPolicies) {
		return fmt.Errorf("resource URI not allowed: %s", uri)
	}
	if !resourceURIAllowedByPolicies(baseURI, len(requestAllowedNames) > 0, requestNameAllowURIs) {
		return fmt.Errorf("resource URI not allowed: %s", uri)
	}
	return nil
}

func (i *toolCallInfo) Service() string {
	return i.service
}

func (i *toolCallInfo) Method() string {
	return i.method
}

func (i *toolCallInfo) Tool() string {
	return i.tool
}

func (i *toolCallInfo) CallType() loom.InterceptorCallType {
	return loom.InterceptorUnary
}

func (i *toolCallInfo) RawPayload() any {
	return i.rawPayload
}

func (i *toolCallInfo) RawArguments() jsontext.Value {
	return i.rawArgs
}

func dispatchSkillResource[P, R any](ctx context.Context, uri, baseURI string, config ResourceDispatchConfig[P, R]) (R, error) {
	var zero R
	resources, err := mcpskills.List(ctx, config.SkillSources)
	if err != nil {
		return zero, mapDispatchError(config.MapError, err, "internal_error", "Unable to inspect skill resource policy.")
	}
	nameToURI := make(map[string]string, len(resources))
	for _, resource := range resources {
		policyURI := resource.URI
		if strings.HasSuffix(policyURI, "/SKILL.md") {
			policyURI = strings.TrimSuffix(policyURI, "SKILL.md")
		}
		nameToURI[resource.Name] = policyURI
	}
	if err := config.Policy.Authorize(ctx, uri, nameToURI); err != nil {
		return zero, mapDispatchError(config.MapError, err, "invalid_params", "Resource URI is not allowed.")
	}
	content, err := mcpskills.Read(ctx, config.SkillSources, baseURI)
	if err != nil {
		if config.Log != nil {
			config.Log(ctx, "error", map[string]any{"method": "resources/read", "uri": baseURI, "error": err.Error()})
		}
		code := "internal_error"
		if errors.Is(err, mcpskills.ErrInvalidURI) {
			code = "invalid_params"
		} else if errors.Is(err, mcpskills.ErrNotFound) {
			code = "resource_not_found"
		}
		message := fmt.Sprintf("Unable to read skill resource: %s", baseURI)
		return zero, mapDispatchError(config.MapError, loom.PermanentError(code, "%s", message), code, message)
	}
	if config.SkillResult == nil {
		return zero, mapDispatchError(config.MapError, fmt.Errorf("MCP skill resource descriptor has no result codec"), "internal_error", "Unable to read skill resource.")
	}
	result := config.SkillResult(content)
	logDispatch(config.Log, ctx, "response", "resources/read", "uri", baseURI)
	return result, nil
}

func mapDispatchError(mapper func(error, string, string) error, err error, code, message string) error {
	if mapper != nil {
		return mapper(err, code, message)
	}
	return loom.PermanentError(code, "%s", message)
}

func logDispatch(logger func(context.Context, string, any), ctx context.Context, event, method, key, value string) {
	if logger == nil {
		return
	}
	logger(ctx, event, map[string]any{"method": method, key: value})
}

func splitResourceNames(names string) []string {
	if names == "" {
		return nil
	}
	return strings.Split(names, ",")
}

func resolveResourceNames(names []string, extra, generated map[string]string) []string {
	resolved := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		uri, ok := extra[name]
		if !ok {
			uri, ok = generated[name]
		}
		if ok {
			resolved = append(resolved, uri)
		}
	}
	return resolved
}

func resourceURIAllowedByPolicies(uri string, configured bool, policies []string) bool {
	if !configured {
		return true
	}
	for _, policy := range policies {
		if resourceURIMatchesPolicy(uri, policy) {
			return true
		}
	}
	return false
}

func resourceURIMatchesPolicy(uri, policy string) bool {
	policy = strings.TrimSpace(policy)
	return uri == policy || strings.HasSuffix(policy, "/") && strings.HasPrefix(uri, policy)
}
