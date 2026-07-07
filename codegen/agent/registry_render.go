package codegen

import (
	"bytes"
	"text/template"

	"github.com/CaliLuke/loom/codegen"
)

func agentRegistrySection(data struct {
	*AgentData
	HasExternalMCP bool
}) codegen.Section {
	return codegen.MustRenderSection("agent-registry", func() string {
		tpl := template.Must(template.New("agent-registry").Funcs(templateFuncMap()).Parse(agentRegistryTemplateSource))
		var buf bytes.Buffer
		if err := tpl.Execute(&buf, data); err != nil {
			panic(err)
		}
		return buf.String()
	})
}

const agentRegistryTemplateSource = `{{- define "activityOptionsLiteral" -}}
engine.ActivityOptions{
{{- if ne .Queue "" }}
    Queue: {{ printf "%q" .Queue }},
{{- end }}
{{- if gt .ScheduleToStartTimeout 0 }}
    ScheduleToStartTimeout: time.Duration({{ printf "%d" .ScheduleToStartTimeout }}),
{{- end }}
{{- if gt .StartToCloseTimeout 0 }}
    StartToCloseTimeout: time.Duration({{ printf "%d" .StartToCloseTimeout }}),
{{- end }}
{{- if gt .HeartbeatTimeout 0 }}
    HeartbeatTimeout: time.Duration({{ printf "%d" .HeartbeatTimeout }}),
{{- end }}
{{- if or (gt .RetryPolicy.MaxAttempts 0) (gt .RetryPolicy.InitialInterval 0) (ne .RetryPolicy.BackoffCoefficient 0.0) }}
    RetryPolicy: engine.RetryPolicy{
{{- if gt .RetryPolicy.MaxAttempts 0 }}
        MaxAttempts: {{ .RetryPolicy.MaxAttempts }},
{{- end }}
{{- if gt .RetryPolicy.InitialInterval 0 }}
        InitialInterval: time.Duration({{ printf "%d" .RetryPolicy.InitialInterval }}),
{{- end }}
{{- if ne .RetryPolicy.BackoffCoefficient 0.0 }}
        BackoffCoefficient: {{ printf "%g" .RetryPolicy.BackoffCoefficient }},
{{- end }}
    },
{{- end }}
}
{{- end }}

// Register{{ .StructName }} registers the generated agent components with the runtime.
func Register{{ .StructName }}(ctx context.Context, rt *agentsruntime.Runtime, cfg {{ .ConfigType }}) error {
    if rt == nil {
        return errors.New("runtime is required")
    }
    agent, err := New{{ .StructName }}(cfg)
    if err != nil {
        return err
    }
    if err := rt.RegisterAgent(ctx, agentsruntime.AgentRegistration{
        ID:      {{ printf "%q" .ID }},
        Planner: agent.Planner,
        Workflow: engine.WorkflowDefinition{
            Name:      {{ printf "%q" .Runtime.Workflow.Name }},
            TaskQueue: {{ printf "%q" .Runtime.Workflow.Queue }},
            Handler:   rt.ExecuteWorkflow,
        },
{{- if .Runtime.PlanActivity }}
        PlanActivityName: {{ printf "%q" .Runtime.PlanActivity.Name }},
        PlanActivityOptions: {{ template "activityOptionsLiteral" .Runtime.PlanActivity }},
{{- end }}
{{- if .Runtime.ResumeActivity }}
        ResumeActivityName: {{ printf "%q" .Runtime.ResumeActivity.Name }},
        ResumeActivityOptions: {{ template "activityOptionsLiteral" .Runtime.ResumeActivity }},
{{- end }}
{{- if .Runtime.ExecuteTool }}
        ExecuteToolActivity: {{ printf "%q" .Runtime.ExecuteTool.Name }},
        ExecuteToolActivityOptions: {{ template "activityOptionsLiteral" .Runtime.ExecuteTool }},
{{- end }}
        {{- if .Tools }}
        Specs: {{ .ToolSpecsPackage }}.Specs,
        {{- else }}
        Specs: nil,
        {{- end }}
        Policy: agentsruntime.RunPolicy{
{{- if gt .RunPolicy.Caps.MaxToolCalls 0 }}
            MaxToolCalls: {{ .RunPolicy.Caps.MaxToolCalls }},
{{- end }}
{{- if gt .RunPolicy.Caps.MaxConsecutiveFailedToolCalls 0 }}
            MaxConsecutiveFailedToolCalls: {{ .RunPolicy.Caps.MaxConsecutiveFailedToolCalls }},
{{- end }}
{{- if gt .RunPolicy.TimeBudget 0 }}
            TimeBudget: time.Duration({{ printf "%d" .RunPolicy.TimeBudget }}),
{{- end }}
{{- if .RunPolicy.InterruptsAllowed }}
            InterruptsAllowed: true,
{{- end }}
{{- if .RunPolicy.NamedInterceptors }}
            NamedInterceptors: []string{ {{- range $idx, $id := .RunPolicy.NamedInterceptors }}{{ if $idx }}, {{ end }}{{ printf "%q" $id }}{{- end }}},
{{- end }}
{{- if .RunPolicy.OnMissingFields }}
            {{- if eq .RunPolicy.OnMissingFields "finalize" }}
            OnMissingFields: agentsruntime.MissingFieldsFinalize,
            {{- else if eq .RunPolicy.OnMissingFields "await_clarification" }}
            OnMissingFields: agentsruntime.MissingFieldsAwaitClarification,
            {{- else if eq .RunPolicy.OnMissingFields "resume" }}
            OnMissingFields: agentsruntime.MissingFieldsResume,
            {{- end }}
{{- end }}
{{- if .RunPolicy.History }}
            History: func() agentsruntime.HistoryPolicy {
            {{- if eq .RunPolicy.History.Mode "keep_recent" }}
                return agentsruntime.KeepRecentTurns({{ .RunPolicy.History.KeepRecent }})
            {{- else if eq .RunPolicy.History.Mode "compress" }}
                return agentsruntime.Compress(cfg.HistoryModel, agentsruntime.HistoryCompressionConfig{
                {{- if gt .RunPolicy.History.CompressAtTurns 0 }}
                    CompressAtTurns: {{ .RunPolicy.History.CompressAtTurns }},
                {{- end }}
                {{- if gt .RunPolicy.History.CompressAtMaxInputTokens 0 }}
                    CompressAtMaxInputTokens: {{ .RunPolicy.History.CompressAtMaxInputTokens }},
                {{- end }}
                {{- if gt .RunPolicy.History.KeepMaxTurns 0 }}
                    KeepMaxTurns: {{ .RunPolicy.History.KeepMaxTurns }},
                {{- end }}
                {{- if gt .RunPolicy.History.KeepMaxInputTokens 0 }}
                    KeepMaxInputTokens: {{ .RunPolicy.History.KeepMaxInputTokens }},
                {{- end }}
                })
            {{- end }}
            }(),
{{- end }}
{{- if or .RunPolicy.Cache.AfterSystem .RunPolicy.Cache.AfterTools }}
            Cache: agentsruntime.CachePolicy{
            {{- if .RunPolicy.Cache.AfterSystem }}
                AfterSystem: true,
            {{- end }}
            {{- if .RunPolicy.Cache.AfterTools }}
                AfterTools: true,
            {{- end }}
            },
{{- end }}
{{- if .RunPolicy.PreloadMemory }}
            PreloadMemory: &agentsruntime.MemoryPreloadPolicy{
            {{- if eq .RunPolicy.PreloadMemory.Scope "current_run" }}
                Scope: agentsruntime.MemoryScopeCurrentRun,
            {{- else if eq .RunPolicy.PreloadMemory.Scope "indexed" }}
                Scope: agentsruntime.MemoryScopeIndexed,
            {{- end }}
                MaxResults: {{ .RunPolicy.PreloadMemory.MaxResults }},
            },
{{- end }}
{{- if .RunPolicy.PreloadLongTermMemory }}
            PreloadLongTermMemory: &agentsruntime.LongTermMemoryPreloadPolicy{
            {{- if eq .RunPolicy.PreloadLongTermMemory.Visibility "shared" }}
                Visibility: memory.VisibilityShared,
            {{- else }}
                Visibility: memory.VisibilityUser,
            {{- end }}
                MaxResults: {{ .RunPolicy.PreloadLongTermMemory.MaxResults }},
            },
{{- end }}
        },
{{- if .RunPolicy.RetryAndReflect }}
        Interceptors: []agentsruntime.Interceptor{
            agentsruntime.NewRetryAndReflectInterceptor(agentsruntime.RetryAndReflectConfig{
{{- if gt .RunPolicy.RetryAndReflect.MaxRetries 0 }}
                MaxRetries: {{ .RunPolicy.RetryAndReflect.MaxRetries }},
{{- end }}
{{- if .RunPolicy.RetryAndReflect.ErrorIfRetryExceeded }}
                ErrorIfRetryExceeded: true,
{{- end }}
            }),
        },
{{- end }}
    }); err != nil {
        return err
    }

    {{- if .HasExternalMCP }}
    // Register MCP-backed toolsets using local executors and callers from config.
    if cfg.MCPCallers == nil {
        return fmt.Errorf("mcp callers are required for agent %s", {{ printf "%q" .ID }})
    }
    {{- range .AllToolsets }}
    {{- if isMCPBacked . }}
    {
        caller := cfg.MCPCallers[{{ .MCP.ConstName }}]
        if caller == nil {
            return fmt.Errorf("mcp caller for %s is required", {{ .MCP.ConstName }})
        }
        exec := {{ .PackageName }}.New{{ $.GoName }}{{ goify .PathName true }}MCPExecutor(caller)
        // Build a runtime ToolsetRegistration inline to avoid exposing method/service adapters.
        reg := agentsruntime.ToolsetRegistration{
            Name: {{ printf "%q" .QualifiedName }},
            // Use the used-toolset specs package for strong-contract payload/result codecs.
            Specs: {{ .SpecsPackageName }}.Specs,
            Execute: func(ctx context.Context, call *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
                if call == nil {
                    return nil, fmt.Errorf("tool request is nil")
                }
                meta := &agentsruntime.ToolCallMeta{
                    RunID:            call.RunID,
                    SessionID:        call.SessionID,
                    TurnID:           call.TurnID,
                    ToolCallID:       call.ToolCallID,
                    ParentToolCallID: call.ParentToolCallID,
                }
                result, err := exec.Execute(ctx, meta, call)
                if err != nil {
                    return nil, err
                }
                if result == nil {
                    return nil, fmt.Errorf("executor returned nil execution result")
                }
                return result, nil
            },
        }
        if err := rt.RegisterToolset(reg); err != nil {
            return err
        }
    }
    {{- end }}
    {{- end }}
    {{- end }}

    // Service-backed toolsets (method-backed Used toolsets) are registered by
    // application code using executors. Agent-exported toolsets are wired via
    // provider agenttools helpers and consumer-side agent toolset helpers.
    return nil
}

{{- $had := false -}}
{{- $hasExecutorBacked := false -}}
{{- range .UsedToolsets }}
{{- if needsUsedToolsetRegistration . }}
{{- $had = true -}}
{{- end }}
{{- if needsExecutorBackedRegistration . }}
{{- $hasExecutorBacked = true -}}
{{- end }}
{{- end }}
{{- if $had }}
// RegisterUsedToolsets registers all non-MCP Used toolsets for this agent.
{{- if $hasExecutorBacked }}
// Provide executors via typed options for each executor-backed toolset.
//
// Example:
//   err := RegisterUsedToolsets(ctx, rt,
{{- range .UsedToolsets }}
{{- if needsExecutorBackedRegistration . }}
//       With{{ goify .PathName true }}Executor(exec),
{{- end }}
{{- end }}
//   )
{{- end }}
func RegisterUsedToolsets(ctx context.Context, rt *agentsruntime.Runtime, opts ...func(map[string]agentsruntime.ToolCallExecutor)) error {
    if rt == nil {
        return errors.New("runtime is required")
    }
    {{- if $hasExecutorBacked }}
    execs := make(map[string]agentsruntime.ToolCallExecutor)
    for _, o := range opts {
        if o != nil {
            o(execs)
        }
    }
    {{- end }}
    // Register non-MCP used toolsets that are not provided by agent-as-tool exports.
    {{- range .UsedToolsets }}
    {{- if needsRuntimeBackedRegistration . }}
    {{- if isSkillsBacked . }}
    {
        reg := agentsruntime.NewSkillToolsetRegistration(agentsruntime.SkillToolsetConfig{
            Name: {{ printf "%q" .QualifiedName }},
            Roots: []string{
            {{- range .Expr.Provider.SkillRoots }}
                {{ printf "%q" . }},
            {{- end }}
            },
            Preload: {{ skillPreloadRef .Expr.Provider.SkillPreload }},
            Reload: {{ skillReloadRef .Expr.Provider.SkillReload }},
        })
        if err := rt.RegisterToolset(reg); err != nil {
            return err
        }
    }
    {{- else if isArtifactsBacked . }}
    {
        reg := agentsruntime.NewArtifactToolsetRegistration(agentsruntime.ArtifactToolsetConfig{
            Store: rt.ArtifactStore,
            Name: {{ printf "%q" .QualifiedName }},
            MaxArtifactBytes: {{ .Expr.Provider.ArtifactMaxBytes }},
            MaxArtifacts: {{ .Expr.Provider.ArtifactMaxCount }},
        })
        if err := rt.RegisterToolset(reg); err != nil {
            return err
        }
    }
    {{- else if isMemoryBacked . }}
    {
        reg := agentsruntime.NewMemoryToolsetRegistration(agentsruntime.MemoryToolsetConfig{
            Store: rt.Memory,
            Searcher: rt.MemorySearcher,
            {{- if memoryToolsetUsesLongTerm . }}
            Service: rt.MemoryService,
            ScopeResolver: rt.MemoryScopeResolver,
            {{- end }}
            Name: {{ printf "%q" .QualifiedName }},
            {{- if .Expr.Provider.MemorySources }}
            Sources: []memory.ToolSource{
            {{- range .Expr.Provider.MemorySources }}
                {{ memoryToolSourceRef . }},
            {{- end }}
            },
            {{- end }}
            {{- if .Expr.Provider.MemoryVisibility }}
            Visibility: {{ memoryVisibilityRef .Expr.Provider.MemoryVisibility }},
            {{- end }}
            MaxResults: {{ .Expr.Provider.MemoryMaxResults }},
        })
        if err := rt.RegisterToolset(reg); err != nil {
            return err
        }
    }
    {{- end }}
    {{- end }}
    {{- if needsExecutorBackedRegistration . }}
    {
        const toolsetID = {{ printf "%q" .QualifiedName }}
        exec := execs[toolsetID]
        reg := agentsruntime.ToolsetRegistration{
            Name:  toolsetID,
            Specs: {{ .SpecsPackageName }}.Specs,
            Execute: func(ctx context.Context, call *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
                if call == nil {
                    return nil, fmt.Errorf("tool request is nil")
                }
                if exec == nil {
                    return agentsruntime.Executed(&planner.ToolResult{
                        Error: planner.NewToolError(
                            fmt.Sprintf(
                                "no executor registered for toolset %q; ensure the appropriate With...Executor is wired in RegisterUsedToolsets",
                                toolsetID,
                            ),
                        ),
                    }), nil
                }
                meta := &agentsruntime.ToolCallMeta{
                    RunID:            call.RunID,
                    SessionID:        call.SessionID,
                    TurnID:           call.TurnID,
                    ToolCallID:       call.ToolCallID,
                    ParentToolCallID: call.ParentToolCallID,
                }
                result, err := exec.Execute(ctx, meta, call)
                if err != nil {
                    return nil, err
                }
                if result == nil {
                    return nil, fmt.Errorf("executor returned nil execution result")
                }
                return result, nil
            },
        }
        {{- $hasCallHints := false -}}
        {{- $hasResultHints := false -}}
        {{- range .Tools }}
        {{- if .CallHintTemplate }}{{- $hasCallHints = true -}}{{- end }}
        {{- if .ResultHintTemplate }}{{- $hasResultHints = true -}}{{- end }}
        {{- end }}
        {{- if or $hasCallHints $hasResultHints }}
        // Install DSL-provided hint templates when present.
        {
            {{- if $hasCallHints }}
            {
                compiled, err := hints.CompileHintTemplates(map[tools.Ident]string{
                {{- range .Tools }}
                {{- if .CallHintTemplate }}
                    // Use the canonical tool identifier so hints align with Specs and runtime events.
                    tools.Ident({{ printf "%q" .QualifiedName }}): {{ printf "%q" .CallHintTemplate }},
                {{- end }}
                {{- end }}
                }, nil)
                if err != nil {
                    return err
                }
                reg.CallHints = compiled
            }
            {{- end }}
            {{- if $hasResultHints }}
            {
                compiled, err := hints.CompileHintTemplates(map[tools.Ident]string{
                {{- range .Tools }}
                {{- if .ResultHintTemplate }}
                    // Use the canonical tool identifier so hints align with Specs and runtime events.
                    tools.Ident({{ printf "%q" .QualifiedName }}): {{ printf "%q" .ResultHintTemplate }},
                {{- end }}
                {{- end }}
                }, nil)
                if err != nil {
                    return err
                }
                reg.ResultHints = compiled
            }
            {{- end }}
        }
        {{- end }}
        if err := rt.RegisterToolset(reg); err != nil {
            return err
        }
    }
    {{- end }}
    {{- end }}
    return nil
}

    {{- range .UsedToolsets }}
    {{- if needsExecutorBackedRegistration . }}
// With{{ goify .PathName true }}Executor associates an executor for {{ .QualifiedName }}.
func With{{ goify .PathName true }}Executor(exec agentsruntime.ToolCallExecutor) func(map[string]agentsruntime.ToolCallExecutor) {
    return func(m map[string]agentsruntime.ToolCallExecutor) {
        if exec == nil {
            return
        }
        m[{{ printf "%q" .QualifiedName }}] = exec
    }
}
{{- end }}
{{- end }}
{{- end }}
`
