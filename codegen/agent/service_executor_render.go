package codegen

import (
	"bytes"
	"text/template"

	"github.com/CaliLuke/loom/codegen"
)

func serviceExecutorSection(data serviceToolsetFileData) codegen.Section {
	return codegen.NewRenderSection("service-executor", func() string {
		tpl := template.Must(template.New("service-executor").Funcs(templateFuncMap()).Parse(serviceExecutorTemplateSource))
		var buf bytes.Buffer
		if err := tpl.Execute(&buf, data); err != nil {
			panic(err)
		}
		return buf.String()
	})
}

const serviceExecutorTemplateSource = `// Default service executor for {{ .Toolset.Name }}
// This factory builds a runtime.ToolCallExecutor that dispatches tool calls to
// user-provided per-tool callers. It decodes tool payloads with generated codecs,
// allows optional payload/result mappers, and returns results as-is (or mapped).
//
// The executor automatically wires the provided service client to the tool callers.
// You can override individual callers using the generated With<Tool> options.
//
// Example:
//
//   client := atlasdata.NewClient(...)
//   exec := {{ .Toolset.PackageName }}.New{{ .Agent.GoName }}{{ goify .Toolset.PathName true }}Exec(client)
//
//   // Register:
//   // reg := {{ .Agent.GoName }}{{ goify .Toolset.PathName true }}.New{{ .Agent.GoName }}{{ goify .Toolset.PathName true }}ToolsetRegistration(exec)
//   // rt.RegisterToolset(reg)

type (
    seCfg struct {
        callers    map[tools.Ident]func(context.Context, any) (any, error)
        mapPayload func(tools.Ident, any, *runtime.ToolCallMeta) (any, error)
        mapResult  func(tools.Ident, any, *runtime.ToolCallMeta) (any, error)
        injectors  []ToolInterceptor
    }
    // ExecOpt customizes the default service executor.
    ExecOpt interface{ apply(*seCfg) }

    // ToolInterceptor hooks into tool execution to inject context or modify payloads.
    ToolInterceptor interface {
        // Inject mutates the service method payload before the client call.
        // It receives the fully mapped service payload (e.g. *GetAlarmsPayload)
        // and the tool call metadata.
        Inject(ctx context.Context, payload any, meta *runtime.ToolCallMeta) error
    }
    
    ToolInterceptorFunc func(context.Context, any, *runtime.ToolCallMeta) error
)

func (f ToolInterceptorFunc) Inject(ctx context.Context, p any, m *runtime.ToolCallMeta) error {
    return f(ctx, p, m)
}

func dispatchInjectors(interceptors []ToolInterceptor) []func(context.Context, any, *runtime.ToolCallMeta) error {
    if len(interceptors) == 0 {
        return nil
    }
    injectors := make([]func(context.Context, any, *runtime.ToolCallMeta) error, 0, len(interceptors))
    for _, interceptor := range interceptors {
        if interceptor == nil {
            continue
        }
        injectors = append(injectors, interceptor.Inject)
    }
    return injectors
}

type execOptFunc func(*seCfg)

func (f execOptFunc) apply(c *seCfg) { f(c) }

// WithPayloadMapper installs a mapper for tool payload -> method payload.
func WithPayloadMapper(f func(tools.Ident, any, *runtime.ToolCallMeta) (any, error)) ExecOpt {
    return execOptFunc(func(c *seCfg) { c.mapPayload = f })
}

// WithResultMapper installs a mapper for method result -> tool result.
func WithResultMapper(f func(tools.Ident, any, *runtime.ToolCallMeta) (any, error)) ExecOpt {
    return execOptFunc(func(c *seCfg) { c.mapResult = f })
}

// WithInterceptors adds interceptors to the executor.
func WithInterceptors(interceptors ...ToolInterceptor) ExecOpt {
    return execOptFunc(func(c *seCfg) {
        c.injectors = append(c.injectors, interceptors...)
    })
}

// WithClient wires default callers for all method-backed tools using the
// provided service client. This is a convenience for direct service wiring;
// adapter-style executors can instead provide callers via the With<Tool>
// options without supplying a client.
func WithClient(client *{{ .ServicePkgAlias }}.Client) ExecOpt {
    return execOptFunc(func(c *seCfg) {
        if client == nil {
            return
        }
        if c.callers == nil {
            c.callers = make(map[tools.Ident]func(context.Context, any) (any, error))
        }
        {{- range .Toolset.Tools }}
        {{- if .IsMethodBacked }}
        c.callers[tools.Ident({{ printf "%q" .QualifiedName }})] = func(ctx context.Context, args any) (any, error) {
            {{- if .MethodPayloadTypeRef }}
            return client.{{ .MethodGoName }}(ctx, args.({{ .MethodPayloadTypeRef }}))
            {{- else }}
            return client.{{ .MethodGoName }}(ctx)
            {{- end }}
        }
        {{- end }}
        {{- end }}
    })
}

{{- range .Toolset.Tools }}
{{- if .IsMethodBacked }}
// With{{ goify .Name true }} sets the caller for {{ .QualifiedName }}.
func With{{ goify .Name true }}(f func(context.Context, any) (any, error)) ExecOpt {
    return execOptFunc(func(c *seCfg) {
        if c.callers == nil {
            c.callers = make(map[tools.Ident]func(context.Context, any) (any, error))
        }
        c.callers[tools.Ident({{ printf "%q" .QualifiedName }})] = f
    })
}
{{- end }}
{{- end }}

// New{{ .Agent.GoName }}{{ goify .Toolset.PathName true }}Exec returns a ToolCallExecutor that
// decodes tool payloads with generated codecs, applies optional mappers, calls user-provided
// per-tool callers (wired from the client via WithClient), and maps results back.
func New{{ .Agent.GoName }}{{ goify .Toolset.PathName true }}Exec(opts ...ExecOpt) runtime.ToolCallExecutor {
    var cfg seCfg
    cfg.callers = make(map[tools.Ident]func(context.Context, any) (any, error))

    for _, o := range opts {
        if o != nil {
            o.apply(&cfg)
        }
    }
    // Preflight: ensure callers are provided for all method-backed tools.
    {
        var missing []string
        {{- range .Toolset.Tools }}
        {{- if .IsMethodBacked }}
        if cfg.callers == nil || cfg.callers[tools.Ident({{ printf "%q" .QualifiedName }})] == nil {
            // report the fully-qualified tool for clarity
            missing = append(missing, {{ printf "%q" .QualifiedName }})
        }
        {{- end }}
        {{- end }}
        if len(missing) > 0 {
            panic(fmt.Errorf("service executor missing callers for tools: %s", strings.Join(missing, ", ")))
        }
    }
    return runtime.ToolCallExecutorFunc(func(ctx context.Context, meta *runtime.ToolCallMeta, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
        if call == nil {
            return runtime.Executed(&planner.ToolResult{Error: planner.NewToolError("tool request is nil")}), nil
        }
        if meta == nil {
            return runtime.Executed(&planner.ToolResult{Error: planner.NewToolError("tool call meta is nil")}), nil
        }
        // Lookup caller registered for this tool.
        caller := cfg.callers[call.Name]
        if caller == nil {
            return runtime.Executed(&planner.ToolResult{
                Name: call.Name,
                Error: planner.NewToolError(
                    fmt.Sprintf(
                        "no service caller registered for tool %q in toolset %q; "+
                            "ensure the appropriate With... option is wired when constructing the executor",
                        call.Name,
                        "{{ .Toolset.QualifiedName }}",
                    ),
                ),
            }), nil
        }
        {{- if .Toolset.Tools }}
        switch call.Name {
        {{- range .Toolset.Tools }}
        {{- if .IsMethodBacked }}
        case tools.Ident({{ printf "%q" .QualifiedName }}):
            result, err := {{ $.Toolset.SpecsPackageName }}.Dispatch{{ .ConstName }}Method(ctx, meta, json.RawMessage(call.Payload), call.Labels, {{ $.Toolset.SpecsPackageName }}.{{ .ConstName }}DispatchOptions{
                Call: caller,
                MapPayload: cfg.mapPayload,
                MapResult: cfg.mapResult,
                Injectors: dispatchInjectors(cfg.injectors),
            })
            if err != nil {
                return nil, err
            }
            return runtime.Executed(result), nil
        {{- end }}
        {{- end }}
        default:
            return runtime.Executed(&planner.ToolResult{
                Name: call.Name,
                Error: planner.NewToolError(
                    fmt.Sprintf("tool %q is not a method-backed tool in toolset %q", call.Name, "{{ .Toolset.QualifiedName }}"),
                ),
            }), nil
        }
        {{- end }}
    })
}
`
