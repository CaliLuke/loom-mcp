package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextcompat"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type compatibilityResultContext struct {
	*testWorkflowContext
	output *api.RecordContentOutput
}

func (w *compatibilityResultContext) ExecuteRecordContentActivity(context.Context, engine.RecordContentActivityCall) (*api.RecordContentOutput, error) {
	return w.output, nil
}

func TestNonTextCompatibilityRejectsUnusableActivityResults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*contextcompat.Result) *api.RecordContentOutput
	}{
		{"missing output", func(*contextcompat.Result) *api.RecordContentOutput {
			return nil
		}},
		{"missing result", func(*contextcompat.Result) *api.RecordContentOutput {
			return &api.RecordContentOutput{}
		}},
		{"missing ref", func(r *contextcompat.Result) *api.RecordContentOutput {
			r.Ref = ""
			return &api.RecordContentOutput{Compatibility: r}
		}},
		{"unchecked", func(r *contextcompat.Result) *api.RecordContentOutput {
			r.Evidence.Checked = false
			return &api.RecordContentOutput{Compatibility: r}
		}},
		{"stale coverage", func(r *contextcompat.Result) *api.RecordContentOutput {
			r.Evidence.Coverage = "old"
			return &api.RecordContentOutput{Compatibility: r}
		}},
		{"stale support", func(r *contextcompat.Result) *api.RecordContentOutput {
			r.Evidence.Support = "old"
			return &api.RecordContentOutput{Compatibility: r}
		}},
		{"invalid rejection", func(r *contextcompat.Result) *api.RecordContentOutput {
			r.Evidence.Compatible = false
			r.Evidence.Reason = "arbitrary"
			return &api.RecordContentOutput{Compatibility: r}
		}},
		{"missing witness", func(r *contextcompat.Result) *api.RecordContentOutput {
			r.Evidence.Compatible = false
			r.Evidence.Reason = "provider"
			return &api.RecordContentOutput{Compatibility: r}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			result := &contextcompat.Result{Ref: "fact", Evidence: compaction.Compatibility{Coverage: "current", Support: "current", Checked: true, Compatible: true}}
			wf := &compatibilityResultContext{testWorkflowContext: &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}, output: tc.alter(result)}
			_, err := rt.recordNonTextCompatibility(wf, &PlanActivityInput{AgentID: "agent", RunID: "run"}, engine.ActivityOptions{}, contextcompat.Request{Scope: "root", RunID: "run", Coverage: "current", Support: "current"})
			require.Error(t, err)
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			assert.Empty(t, page.Events, "unusable activity output must never be adopted")
		})
	}
}
