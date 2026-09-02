// Package codegen isolates runtime policy artifacts from structural generator
// data assembly.
//
// This file owns the small cluster of helpers that translate DSL run-policy
// settings into activity/runtime metadata. Keeping them separate lets `data.go`
// stay focused on shape assembly while preserving the same package-local
// contracts and defaults for activity generation.
package codegen

import (
	"fmt"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/codegen/naming"
	agentsExpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
)

// newRunPolicyData copies the evaluated DSL run policy into immutable template
// data, preserving only the fields that affect generated runtime wiring.
func newRunPolicyData(expr *agentsExpr.RunPolicyExpr) RunPolicyData {
	rp := RunPolicyData{
		Caps: CapsData{MaxRecoveryTurns: policy.DefaultMaxRecoveryTurns},
	}
	if expr == nil {
		return rp
	}
	rp.TimeBudget = expr.TimeBudget
	rp.PlanTimeout = expr.PlanTimeout
	rp.ToolTimeout = expr.ToolTimeout
	rp.InterruptsAllowed = expr.InterruptsAllowed
	rp.OnMissingFields = expr.OnMissingFields
	rp.NamedInterceptors = append([]string(nil), expr.Interceptors...)
	if expr.History != nil {
		h := &HistoryData{
			Mode:                     string(expr.History.Mode),
			KeepRecent:               expr.History.KeepRecent,
			CompressAtTurns:          expr.History.CompressAtTurns,
			CompressAtMaxInputTokens: expr.History.CompressAtMaxInputTokens,
			KeepMaxTurns:             expr.History.KeepMaxTurns,
			KeepMaxInputTokens:       expr.History.KeepMaxInputTokens,
		}
		rp.History = h
	}
	if expr.Cache != nil {
		rp.Cache = CacheData{
			AfterSystem: expr.Cache.AfterSystem,
			AfterTools:  expr.Cache.AfterTools,
		}
	}
	if expr.RetryAndReflect != nil {
		rp.RetryAndReflect = &RetryAndReflectData{
			MaxRetries:           expr.RetryAndReflect.MaxRetries,
			ErrorIfRetryExceeded: expr.RetryAndReflect.ErrorIfRetryExceeded,
		}
	}
	if expr.PreloadMemory != nil {
		rp.PreloadMemory = &MemoryPreloadData{
			Scope:      string(expr.PreloadMemory.Scope),
			MaxResults: expr.PreloadMemory.MaxResults,
		}
	}
	if expr.PreloadLongTermMemory != nil {
		rp.PreloadLongTermMemory = &LongTermMemoryPreloadData{
			Visibility: string(expr.PreloadLongTermMemory.Visibility),
			MaxResults: expr.PreloadLongTermMemory.MaxResults,
		}
	}
	if expr.DefaultCaps != nil {
		rp.Caps.MaxToolCalls = expr.DefaultCaps.MaxToolCalls
		if expr.DefaultCaps.MaxRecoveryTurns > 0 {
			rp.Caps.MaxRecoveryTurns = expr.DefaultCaps.MaxRecoveryTurns
		}
	}
	return rp
}

// newActivity derives the generated activity names, function identifiers, and
// retry policy from one logical agent runtime activity.
func newActivity(agent *AgentData, kind ActivityKind, logicalSuffix string, queue string) ActivityArtifact {
	funcName := fmt.Sprintf("%s%sActivity", agent.GoName, logicalSuffix)
	definitionVar := fmt.Sprintf("%s%sActivityDefinition", agent.GoName, logicalSuffix)
	name := naming.Identifier(agent.Service.Name, agent.Name, strings.ToLower(logicalSuffix))
	artifact := ActivityArtifact{
		Name:          name,
		FuncName:      funcName,
		DefinitionVar: definitionVar,
		Queue:         queue,
		Kind:          kind,
	}
	switch kind {
	case ActivityKindPlan, ActivityKindResume:
		artifact.RetryPolicy = defaultActivityRetryPolicy()
		artifact.StartToCloseTimeout = defaultPlannerActivityTimeout
	case ActivityKindExecuteTool:
		// ExecuteTool retries are safe because logical tool calls now carry stable
		// identities and runtimes/providers are responsible for replaying durable
		// results instead of re-running side effects on retried attempts.
		artifact.RetryPolicy = defaultActivityRetryPolicy()
	}
	return artifact
}

// defaultActivityRetryPolicy returns the shared retry profile for generated
// planner/runtime activities.
func defaultActivityRetryPolicy() engine.RetryPolicy {
	return engine.RetryPolicy{
		MaxAttempts:        3,
		InitialInterval:    time.Second,
		BackoffCoefficient: 2,
	}
}
