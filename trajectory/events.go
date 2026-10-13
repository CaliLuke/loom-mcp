package trajectory

import (
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// observedStepKinds selects observer-only evidence. Stored calls, results and
// usage exclusively own their corresponding record-derived steps and totals.
var observedStepKinds = map[hooks.EventType]StepKind{
	hooks.AwaitClarification: StepAwait,
	hooks.AwaitQuestions:     StepAwait,
	hooks.AwaitTypedInput:    StepAwait,
	hooks.AwaitConfirmation:  StepAwait,
	hooks.AwaitExternalTools: StepAwait,
	hooks.ToolAuthorization:  StepAnswer,
	hooks.ThinkingBlock:      StepEvent,
	hooks.PlannerNote:        StepEvent,
	hooks.RetryHintIssued:    StepEvent,
	hooks.PolicyDecision:     StepEvent,
	hooks.RunPhaseChanged:    StepEvent,
	hooks.RunCompleted:       StepEvent,
	hooks.PromptRendered:     StepEvent,
}

func (r *runBuilder) readEventsAndEntries() error {
	step := 0
	var first, last time.Time
	hasRecord := false
	for _, event := range r.run.Events {
		if !hasRecord && r.out.AgentID == "" {
			r.out.AgentID = event.AgentID
		}
		if !hasRecord && r.out.SessionID == "" {
			r.out.SessionID = event.SessionID
		}
		if hooks.IsRecordEvent(event.Type) {
			if !hasRecord {
				// Canonical identity replaces observer metadata, including an empty session.
				r.out.AgentID = event.AgentID
				r.out.SessionID = event.SessionID
				first = event.Timestamp
				hasRecord = true
			}
			if r.out.AgentID == "" {
				r.out.AgentID = event.AgentID
			}
			last = event.Timestamp
		}
		if event.Type == hooks.PlannerStep {
			next, err := r.readPlannerBoundary(event)
			if err != nil {
				return err
			}
			step = next
		}
		if err := r.readObserved(step, event); err != nil {
			return err
		}
	}
	if hasRecord {
		r.out.Totals.WallTime = last.Sub(first)
	}
	if len(r.run.Entries) > 0 {
		return r.readEntries(step, r.run.Entries[len(r.run.Entries)-1].ID)
	}
	return nil
}

// readPlannerBoundary reads the new input prefix. Assistant entries retain
// their producing call's step.
func (r *runBuilder) readPlannerBoundary(event *runlog.Event) (int, error) {
	var boundary hooks.PlannerStepEvent
	if err := json.Unmarshal(event.Payload, &boundary); err != nil {
		return 0, fmt.Errorf("decode planner step: %w", err)
	}
	end, err := r.headEnd(boundary.IndexHead)
	if err != nil {
		return 0, err
	}
	if err := r.readEntries(boundary.Step, end); err != nil {
		return 0, err
	}
	return boundary.Step, nil
}

// headEnd reads only the last segment to locate a head within Run.Entries.
// RunRecord has already validated and resolved the full immutable chain.
func (r *runBuilder) headEnd(head runstore.Hash) (index.MessageID, error) {
	if head == "" {
		return 0, nil
	}
	data, err := r.owner.content(head)
	if err != nil {
		return 0, err
	}
	var segment index.IndexSegment
	if err := json.Unmarshal(data, &segment); err != nil {
		return 0, fmt.Errorf("decode planner head: %w", err)
	}
	return segment.First + index.MessageID(len(segment.Entries)) - 1, nil
}

// readObserved uses the source stamp when present, otherwise the latest preceding boundary.
func (r *runBuilder) readObserved(step int, event *runlog.Event) error {
	kind := observedStepKinds[event.Type]
	if kind == "" {
		return nil
	}
	var metadata struct {
		PlannerStep *int `json:"planner_step,omitempty"`
	}
	if err := json.Unmarshal(event.Payload, &metadata); err != nil {
		return fmt.Errorf("decode observed planner step: %w", err)
	}
	if metadata.PlannerStep != nil {
		step = *metadata.PlannerStep
	}
	observed := &ObservedEvent{Type: event.Type, EventKey: event.EventKey, Timestamp: event.Timestamp, Payload: event.Payload}
	r.group(step).events = append(r.group(step).events, Step{Kind: kind, Step: step, Verified: !r.opts.EventsUnverified, Event: observed})
	if event.Type == hooks.RunCompleted {
		decoded, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{Type: event.Type, Payload: event.Payload})
		if err != nil {
			return fmt.Errorf("decode run status: %w", err)
		}
		r.out.Status = decoded.(*hooks.RunCompletedEvent).Status
		r.out.StatusVerified = !r.opts.EventsUnverified
	}
	return nil
}

func (r *runBuilder) linkChildren() error {
	for _, event := range r.run.Events {
		if event.Type != hooks.ChildRunLinked {
			continue
		}
		var link hooks.ChildRunLinkedEvent
		if err := json.Unmarshal(event.Payload, &link); err != nil {
			return fmt.Errorf("decode child link: %w", err)
		}
		child, err := r.owner.buildRun(link.ChildRunID, Options{EventsUnverified: r.opts.EventsUnverified})
		if err != nil {
			return err
		}
		tool, ok := r.tools[link.ToolCallID]
		if !ok {
			return fmt.Errorf("child link %q has no matching call", link.ToolCallID)
		}
		tool.Children = append(tool.Children, child)
	}
	return nil
}
