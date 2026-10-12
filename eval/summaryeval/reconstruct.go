package summaryeval

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	rtcompaction "github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	agRuntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

var errViewMismatch = errors.New(reasonViewMismatch)

// Reconstruct restores the production input solely from adopted run evidence.
func Reconstruct(c Cut) (*agRuntime.SummarizeContextInput, error) {
	r := c.run
	if r.ContextFingerprint == "" || r.ContextSettings == nil || r.ContextSettings.Prompt.Template == "" {
		return nil, errors.New("recorded before K17: no pinned summary template")
	}
	pre, post, state, err := recordedViews(c)
	if err != nil {
		return nil, err
	}
	if c.Origin == originSummary && c.summary.ViewRef != nil {
		post = *c.summary.ViewRef
	}
	if max(pre.TailFrom, 1) != c.From || !sameHash(pre.Checkpoint, c.Prev) || post.IndexHead == "" || post.TailFrom != c.To {
		return nil, errViewMismatch
	}
	pre.TailFrom = c.From
	pre.IndexHead = post.IndexHead
	pre.Pending = nil
	in := &agRuntime.SummarizeContextInput{AgentID: c.event.AgentID, RunID: r.RunID, Scope: c.record.RootRunID, SessionID: c.event.SessionID, TurnID: c.event.TurnID, Step: c.Step, ViewRef: pre, From: c.From, To: c.To, Budget: post.Budget, ContextSettings: *r.ContextSettings, ContextFingerprint: r.ContextFingerprint, ContextModel: r.ContextSettings.Summary.Model, PromptID: prompt.Ident(string(c.event.AgentID) + ".context.summary"), Settings: r.ContextSettings.Summary.Settings, Rate: rtcompaction.SummaryRate(state)}
	calls := summaryCalls(c)
	if len(calls) > 0 {
		in.TurnID = calls[0].Started.TurnID
	}
	return in, nil
}

func sameHash(a, b *runstore.Hash) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func recordedViews(c Cut) (index.ViewRef, index.ViewRef, rtcompaction.State, error) {
	r := c.run
	var pre, post index.ViewRef
	pre.TailFrom = 1
	state := rtcompaction.State{}
	for n, event := range r.Events {
		switch {
		case event.Type == hooks.PlannerStep || event.Type == hooks.ContextOverflow:
			var step hooks.PlannerStepEvent
			// Overflow embeds the planner envelope but adds independent fields.
			if err := json.Unmarshal(event.Payload, &step); err != nil {
				return pre, post, state, err
			}
			if step.Step == c.Step && event.Type == hooks.PlannerStep {
				post = step.ViewRef
			}
			if n < c.eventIndex && step.Step < c.Step {
				pre = step.ViewRef
			}
			if step.Step < c.Step {
				var err error
				state, err = observeStep(c.run, state, step)
				if err != nil {
					return pre, post, state, err
				}
			}
		case event.Type == hooks.ContextCompacted:
			var summary hooks.ContextCompactionEvent
			if err := json.Unmarshal(event.Payload, &summary); err != nil {
				return pre, post, state, err
			}
			if n < c.eventIndex && summary.ViewRef != nil {
				pre = *summary.ViewRef
			}
		}
	}
	return pre, post, state, nil
}

func observeStep(r *runrecord.Run, state rtcompaction.State, step hooks.PlannerStepEvent) (rtcompaction.State, error) {
	state.ValidateBudgets = step.ContextOverrides
	var facts []contextmeasure.RequestFacts
	for _, key := range step.CallKeys {
		for _, call := range r.Calls {
			if call.Record.Key == key && call.Class == "adopted" && call.Activity == callrecord.ActivityPlanner && call.Completion != nil && call.Completion.Facts != nil {
				facts = append(facts, *call.Completion.Facts)
			}
		}
	}
	var err error
	state, err = rtcompaction.ObserveEffective(state, facts, *r.ContextSettings)
	if err != nil {
		return state, err
	}
	return state, nil
}

// source reads only the immutable exported content, never the original store.
func (c Cut) source(_ context.Context, h runstore.Hash) ([]byte, error) {
	b, ok := c.record.Content[h]
	if !ok {
		return nil, fmt.Errorf("compaction: missing content %s", h)
	}
	return b, nil
}
