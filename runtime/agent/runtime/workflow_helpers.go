package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
)

// groupToolCallsByTimeout buckets calls by per-tool timeout override (with `*`
// suffix prefix-match support) or falls back to the default timeout when no
// override applies.
//
// The bucketing is deterministic for workflow replay:
//   - Exact tool-name matches take precedence over prefix matches.
//   - Among prefix matches, the longest prefix wins.
//   - Group ordering follows first appearance in the allowed slice.
func (r *Runtime) groupToolCallsByTimeout(allowed []planner.ToolRequest, input *RunInput, defaultTimeout time.Duration) ([][]planner.ToolRequest, []time.Duration) {
	rules := timeoutRulesFromInput(input)
	if rules == nil {
		return [][]planner.ToolRequest{allowed}, []time.Duration{defaultTimeout}
	}
	return bucketToolCallsByTimeout(allowed, rules, defaultTimeout)
}

// executeGroupedToolCalls runs groups of tool calls with their respective
// timeouts and returns all results in the original group order.
func (r *Runtime) executeGroupedToolCalls(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	agentID agent.Ident,
	base *workflowPlanInput,
	expectedChildren int,
	parentTracker *childTracker,
	finishBy time.Time,
	grouped [][]planner.ToolRequest,
	timeouts []time.Duration,
	toolOpts engine.ActivityOptions,
) ([]*ToolExecutionResult, bool, error) {
	if base.content == nil {
		base.content = &workflowContentBatch{}
	}
	wfCtx = contentWorkflowContext{WorkflowContext: wfCtx, batch: base.content}
	var out []*ToolExecutionResult
	timedOutAny := false
	for i := range grouped {
		opt := toolOpts
		if timeouts[i] > 0 {
			opt.StartToCloseTimeout = timeouts[i]
		}
		sub, timedOut, err := r.executeToolCalls(wfCtx, reg.ExecuteToolActivity, opt, agentID, &base.RunContext, base, grouped[i], expectedChildren, parentTracker, finishBy)
		if err != nil {
			return nil, false, err
		}
		out = append(out, sub...)
		if timedOut {
			timedOutAny = true
		}
	}
	return out, timedOutAny, nil
}

type timeoutRule struct {
	prefix  string
	timeout time.Duration
}

type toolTimeoutRules struct {
	exact    map[string]time.Duration
	prefixes []timeoutRule
}

func timeoutRulesFromInput(input *RunInput) *toolTimeoutRules {
	if input == nil || input.Policy == nil || len(input.Policy.PerToolTimeout) == 0 {
		return nil
	}
	rules := &toolTimeoutRules{
		exact:    make(map[string]time.Duration, len(input.Policy.PerToolTimeout)),
		prefixes: make([]timeoutRule, 0, len(input.Policy.PerToolTimeout)),
	}
	for k, v := range input.Policy.PerToolTimeout {
		kn := string(k)
		if strings.HasSuffix(kn, "*") {
			rules.prefixes = append(rules.prefixes, timeoutRule{
				prefix:  strings.TrimSuffix(kn, "*"),
				timeout: v,
			})
			continue
		}
		rules.exact[kn] = v
	}
	sort.Slice(rules.prefixes, func(i, j int) bool {
		if len(rules.prefixes[i].prefix) != len(rules.prefixes[j].prefix) {
			return len(rules.prefixes[i].prefix) > len(rules.prefixes[j].prefix)
		}
		return rules.prefixes[i].prefix < rules.prefixes[j].prefix
	})
	return rules
}

func bucketToolCallsByTimeout(allowed []planner.ToolRequest, rules *toolTimeoutRules, defaultTimeout time.Duration) ([][]planner.ToolRequest, []time.Duration) {
	var grouped [][]planner.ToolRequest
	var timeouts []time.Duration
	groupIndexByTimeout := make(map[time.Duration]int)
	for _, call := range allowed {
		timeout := resolvedToolTimeout(call.Name, rules, defaultTimeout)
		i, ok := groupIndexByTimeout[timeout]
		if !ok {
			i = len(grouped)
			groupIndexByTimeout[timeout] = i
			grouped = append(grouped, nil)
			timeouts = append(timeouts, timeout)
		}
		grouped[i] = append(grouped[i], call)
	}
	return grouped, timeouts
}

func resolvedToolTimeout(name tools.Ident, rules *toolTimeoutRules, defaultTimeout time.Duration) time.Duration {
	if rules == nil {
		return defaultTimeout
	}
	n := string(name)
	if to, ok := rules.exact[n]; ok && to > 0 {
		return to
	}
	for _, r := range rules.prefixes {
		if strings.HasPrefix(n, r.prefix) && r.timeout > 0 {
			return r.timeout
		}
	}
	return defaultTimeout
}

func (r *Runtime) toolResultReminders(tr *planner.ToolResult) []string {
	var reminders []string
	if spec, ok := r.toolSpec(tr.Name); ok && spec.ResultReminder != "" {
		reminders = append(reminders, spec.ResultReminder)
	}
	if rem := retryHintReminder(tr); rem != "" {
		reminders = append(reminders, rem)
	}
	cursorField := ""
	if spec, ok := r.toolSpec(tr.Name); ok && spec.Bounds != nil && spec.Bounds.Paging != nil {
		cursorField = spec.Bounds.Paging.CursorField
	}
	if rem := boundsReminder(tr, cursorField); rem != "" {
		reminders = append(reminders, rem)
	}
	return reminders
}

func (r *Runtime) toolResultContent(tr *planner.ToolResult) (any, error) {
	if tr == nil {
		return nil, nil
	}
	var resultJSON rawjson.Message
	if tr.Result != nil {
		raw, err := r.marshalToolValue(context.Background(), tr.Name, tr.Result, tr.Bounds)
		if err != nil {
			return nil, fmt.Errorf("runtime: encode tool_result for %s: %w", tr.Name, err)
		}
		resultJSON = rawjson.Message(raw)
	}
	errorMessage := ""
	if tr.Error != nil {
		errorMessage = tr.Error.Error()
	}
	return transcript.ProjectToolResultContent(
		resultJSON,
		tr.Bounds,
		r.formatResultPreview(context.Background(), tr.Name, rawjson.Message(nil), tr.Result, tr.Bounds),
		errorMessage,
	)
}

// hardProtectionIfNeeded emits a protection event and signals finalization when
// agent-as-tool calls produced no child tool calls.
func (r *Runtime) hardProtectionIfNeeded(
	ctx context.Context,
	agentID agent.Ident,
	base *workflowPlanInput,
	vals []*planner.ToolResult,
	turnID string,
) (bool, error) {
	var agentToolCount int
	var totalChildren int
	toolNames := make([]tools.Ident, 0, len(vals))
	for _, tr := range vals {
		if spec, ok := r.toolSpec(tr.Name); ok && spec.IsAgentTool {
			agentToolCount++
			toolNames = append(toolNames, tr.Name)
			if tr.ChildrenCount > 0 {
				totalChildren += tr.ChildrenCount
			}
		}
	}
	if agentToolCount > 0 && totalChildren == 0 {
		if err := r.publishHook(
			ctx,
			hooks.NewHardProtectionEvent(
				base.RunContext.RunID,
				agentID,
				base.RunContext.SessionID,
				"agent_tool_no_children",
				agentToolCount,
				totalChildren,
				toolNames,
			),
			turnID,
		); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// buildNextResumeRequest carries only the committed head and one step's entries.
func (r *Runtime) buildNextResumeRequest(agentID agent.Ident, base *workflowPlanInput, nextAttempt *int) (PlanActivityInput, error) {
	resumeCtx := base.RunContext
	resumeCtx.Attempt = *nextAttempt
	*nextAttempt++
	out := PlanActivityInput{AgentID: agentID, RunID: resumeCtx.RunID, RunContext: resumeCtx, View: base.view()}
	return out, enforcePlanActivityInputBudget(out)
}
