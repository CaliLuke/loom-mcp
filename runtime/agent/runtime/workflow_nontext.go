package runtime

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextcompat"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
)

// prepareNonTextCompatibility adopts exact original-part evidence before any
// threshold decision. Workflow code handles identities only, never content I/O.
func (r *Runtime) prepareNonTextCompatibility(wf engine.WorkflowContext, base *workflowPlanInput, input *PlanActivityInput, options engine.ActivityOptions) error {
	settings := base.contextSettings
	if len(base.compactionState.Models) == 0 || !settings.KeepNonTextResident || !compaction.On(base.compactionState, base.Entries, settings) || input.Overflow != nil {
		return nil
	}
	resident, reason, witness := compaction.NonTextResident(base.compactionState, base.Entries, settings)
	if !resident {
		originals := false
		for _, entry := range base.Entries {
			if entry.NonText > 0 {
				originals = true
				break
			}
		}
		if !originals {
			return nil
		}
		request := contextcompat.BuildRequest(base.RunContext.RootRunID, input.RunID, input.View.IndexHead, input.View.Pending, base.Entries, base.compactionState)
		evidence := base.compactionState.Compatibility
		if !evidence.Checked || evidence.Coverage != request.Coverage || evidence.Support != request.Support {
			evidence, err := r.recordNonTextCompatibility(wf, input, options, request)
			if err != nil {
				return err
			}

			base.compactionState.Compatibility = evidence
		}
		resident, reason, witness = compaction.NonTextResident(base.compactionState, base.Entries, settings)
	}
	if !resident {
		return nil
	}
	return r.adoptNonTextResidency(wf, base, input, reason, witness)
}

// adoptNonTextResidency commits the decision before publishing its ordinary diagnostic.
func (r *Runtime) adoptNonTextResidency(wf engine.WorkflowContext, base *workflowPlanInput, input *PlanActivityInput, reason, witness string) error {
	adoption := hooks.NewContextResidencyEvent(base.RunContext.RootRunID, input.RunID, input.AgentID, input.RunContext.SessionID, input.Step, compaction.NonTextCoverage(base.Entries), compaction.SupportFingerprint(base.compactionState), reason, witness, wf.Now())
	if err := r.publishHook(wf.Context(), adoption, input.RunContext.TurnID); err != nil {
		return err
	}
	// Protected adoption commits the state even if ordinary diagnostic delivery fails.
	base.compactionState.Flags.NonTextResident = true
	base.compactionFlags.NonTextResident = true
	event := hooks.NewContextDiagnosticEvent(input.RunID, input.AgentID, input.RunContext.SessionID, hooks.ContextNonTextResident, wf.Now())
	event.Reason, event.Model = reason, witness
	if err := r.publishHook(wf.Context(), event, input.RunContext.TurnID); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) recordNonTextCompatibility(wf engine.WorkflowContext, input *PlanActivityInput, options engine.ActivityOptions, request contextcompat.Request) (compaction.Compatibility, error) {
	out, err := wf.ExecuteRecordContentActivity(wf.Context(), engine.RecordContentActivityCall{Name: RecordContentActivityName(input.AgentID), Options: options, Input: &api.RecordContentInput{RootRunID: request.Scope, Compatibility: &request}})
	if err != nil {
		return compaction.Compatibility{}, fmt.Errorf("record attachment compatibility: %w", err)
	}
	if out == nil || out.Compatibility == nil {
		return compaction.Compatibility{}, fmt.Errorf("attachment compatibility result missing")
	}
	result := out.Compatibility
	evidence := result.Evidence
	if result.Ref == "" || !evidence.Checked || evidence.Coverage != request.Coverage || evidence.Support != request.Support || (!evidence.Compatible && (evidence.Reason != "provider" && evidence.Reason != "too_large" || evidence.Model == "")) {
		return compaction.Compatibility{}, fmt.Errorf("attachment compatibility result is incomplete or stale")
	}
	event := hooks.NewContextCompactionEvent(input.RunID, input.AgentID, input.RunContext.SessionID, input.Step, hooks.ContextCompatibility, "", "", 0, 0, 0, 0, nil, wf.Now())
	event.Compatibility = result
	if err := r.publishHook(wf.Context(), event, input.RunContext.TurnID); err != nil {
		return compaction.Compatibility{}, err
	}
	return evidence, nil
}
