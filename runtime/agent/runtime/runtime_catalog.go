package runtime

import (
	"encoding/json"

	agent "goa.design/goa-ai/runtime/agent"
	"goa.design/goa-ai/runtime/agent/engine"
	"goa.design/goa-ai/runtime/agent/reminder"
	"goa.design/goa-ai/runtime/agent/tools"
)

// addToolsetLocked registers a toolset and its specs without acquiring the lock.
// Caller must hold r.mu.
func (r *Runtime) addToolsetLocked(ts ToolsetRegistration) {
	r.toolsets[ts.Name] = ts
	r.addToolSpecsLocked(ts.Specs)
}

// addToolSpecsLocked registers tool specs without acquiring the lock.
// Caller must hold r.mu.
func (r *Runtime) addToolSpecsLocked(specs []tools.ToolSpec) {
	for _, spec := range specs {
		r.toolSpecs[spec.Name] = spec
	}
}

// toolSpec retrieves a tool spec by fully qualified name. Thread-safe.
func (r *Runtime) toolSpec(name tools.Ident) (tools.ToolSpec, bool) {
	r.mu.RLock()
	spec, ok := r.toolSpecs[name]
	r.mu.RUnlock()
	return spec, ok
}

// ListAgents returns the IDs of registered agents.
func (r *Runtime) ListAgents() []agent.Ident {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.agents) == 0 {
		return nil
	}
	out := make([]agent.Ident, 0, len(r.agents))
	for id := range r.agents {
		out = append(out, id)
	}
	return out
}

// ListToolsets returns the names of registered toolsets.
func (r *Runtime) ListToolsets() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.toolsets) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.toolsets))
	for id := range r.toolsets {
		out = append(out, id)
	}
	return out
}

// ToolSpec returns the registered ToolSpec for the given tool name.
func (r *Runtime) ToolSpec(name tools.Ident) (tools.ToolSpec, bool) {
	return r.toolSpec(name)
}

// ToolSpecsForAgent returns the ToolSpecs registered by the given agent.
func (r *Runtime) ToolSpecsForAgent(agentID agent.Ident) []tools.ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	specs := r.agentToolSpecs[agentID]
	if len(specs) == 0 {
		return nil
	}
	out := make([]tools.ToolSpec, len(specs))
	copy(out, specs)
	return out
}

// addReminder registers a reminder for the given run. It is a no-op when the reminders engine is not configured.
func (r *Runtime) addReminder(runID string, rem reminder.Reminder) {
	if r.reminders == nil || runID == "" {
		return
	}
	r.reminders.AddReminder(runID, rem)
}

// removeReminder removes a reminder by ID for the given run.
func (r *Runtime) removeReminder(runID, id string) {
	if r.reminders == nil || runID == "" || id == "" {
		return
	}
	r.reminders.RemoveReminder(runID, id)
}

// ToolSchema returns the parsed JSON schema for the tool payload when available.
func (r *Runtime) ToolSchema(name tools.Ident) (map[string]any, bool) {
	r.mu.RLock()
	spec, ok := r.toolSpecs[name]
	r.mu.RUnlock()
	if !ok || len(spec.Payload.Schema) == 0 {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(spec.Payload.Schema, &m); err != nil {
		return nil, false
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out, true
}

// OverridePolicy applies a best-effort in-process override of the registered agent policy.
func (r *Runtime) OverridePolicy(agentID agent.Ident, delta RunPolicy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg, ok := r.agents[agentID]
	if !ok {
		return ErrAgentNotFound
	}
	if delta.MaxToolCalls > 0 {
		reg.Policy.MaxToolCalls = delta.MaxToolCalls
	}
	if delta.MaxConsecutiveFailedToolCalls > 0 {
		reg.Policy.MaxConsecutiveFailedToolCalls = delta.MaxConsecutiveFailedToolCalls
	}
	if delta.TimeBudget > 0 {
		reg.Policy.TimeBudget = delta.TimeBudget
	}
	if delta.FinalizerGrace > 0 {
		reg.Policy.FinalizerGrace = delta.FinalizerGrace
	}
	if delta.InterruptsAllowed {
		reg.Policy.InterruptsAllowed = true
	}
	r.agents[agentID] = reg
	return nil
}

func (r *Runtime) storeWorkflowHandle(runID string, handle engine.WorkflowHandle) {
	r.handleMu.Lock()
	if r.runHandles == nil {
		r.runHandles = make(map[string]engine.WorkflowHandle)
	}
	if handle == nil {
		delete(r.runHandles, runID)
	} else {
		r.runHandles[runID] = handle
	}
	r.handleMu.Unlock()
}

func (r *Runtime) workflowHandle(runID string) (engine.WorkflowHandle, bool) {
	r.handleMu.RLock()
	h, ok := r.runHandles[runID]
	r.handleMu.RUnlock()
	return h, ok
}
