package expect

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

type normalizedCall struct {
	model.ToolCall
	execution *trajectory.ToolStep
	verified  bool
}

type normalizedEvent struct {
	*trajectory.ObservedEvent
	verified bool
}

type runSubject struct {
	roots trajectory.Session
}

type topLevelSubject struct {
	inner Subject
}

// Trajectory constructs a subject from one recorded run. Descendant calls
// follow their parent call in the trajectory's canonical child-link order.
func Trajectory(run *trajectory.Trajectory) Subject {
	return &runSubject{roots: trajectory.Session{run}}
}

// Session constructs a subject in caller-supplied run order, including children.
// Listing a child separately from its parent selects that child twice.
func Session(runs trajectory.Session) Subject {
	return &runSubject{roots: runs}
}

// TopLevel excludes descendants from calls, events, completion and budgets.
// Explicit session members remain roots even if they were another run's child.
func TopLevel(s Subject) Subject {
	return &topLevelSubject{inner: s}
}

// normalizeRuns derives the same selected run set for all expectations.
func normalizeRuns(roots trajectory.Session, topLevel bool) *subject {
	s := &subject{roots: roots}
	for _, root := range roots {
		appendRun(s, root, topLevel)
	}
	return s
}

// appendRun inserts descendants immediately after their owning tool call.
func appendRun(s *subject, run *trajectory.Trajectory, topLevel bool) {
	s.runs = append(s.runs, run)
	for _, step := range run.Steps {
		if step.Tool != nil {
			tool := step.Tool
			s.calls = append(s.calls, normalizedCall{ToolCall: model.ToolCall{Name: tool.Name, ID: tool.ToolCallID, Payload: tool.Arguments}, execution: tool, verified: step.Verified})
			if !topLevel {
				for _, child := range tool.Children {
					appendRun(s, child, false)
				}
			}
		}
		if step.Event != nil {
			s.events = append(s.events, normalizedEvent{ObservedEvent: step.Event, verified: step.Verified})
		}
	}
}

func (r *runSubject) normalize() *subject {
	s := normalizeRuns(r.roots, false)
	s.source = r
	return s
}

func (r *topLevelSubject) normalize() *subject {
	s := r.inner.normalize()
	if !s.modelCall {
		s = normalizeRuns(s.roots, true)
	}
	s.source = r
	return s
}
