package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type metadataOnlySessionStore struct{ session.Store }

func TestContinuationRegistrationRequiresStore(t *testing.T) {
	rt := New()
	rt.SessionStore = metadataOnlySessionStore{sessioninmem.New()}
	reg := AgentRegistration{ID: "agent"}
	reg.Policy.ContextCompaction = &ContextCompaction{}
	require.ErrorContains(t, rt.validateRecallRegistration(reg), "ContinuationStore")
	reg.Policy.ContextCompaction = nil
	assert.NoError(t, rt.validateRecallRegistration(reg))
}

func TestContextDefaultCustomStoreStartsFresh(t *testing.T) {
	rt := New()
	rt.SessionStore = metadataOnlySessionStore{sessioninmem.New()}
	out, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: "root", SeedMessages: true, Continue: &api.ContinuationStartInput{RunID: "root", SessionID: "session", Settings: compaction.Settings{Default: true, KeepNonTextResident: true}}})
	require.NoError(t, err)
	require.NotNil(t, out.Continuation)
	assert.Equal(t, "fresh", out.Continuation.Outcome)
	assert.Equal(t, "store_unsupported", out.Continuation.Reason)
}

func TestContextDefaultCustomStoreCompletesWithoutPublication(t *testing.T) {
	for _, window := range []int{6000, 0} {
		t.Run(fmt.Sprint(window), func(t *testing.T) {
			rt := New(WithContextQualification(compaction.Qualification{Config: "qualified-test", Models: []string{"measured"}, Tier: compaction.Evict}))
			rt.SessionStore = metadataOnlySessionStore{sessioninmem.New()}
			provider := &defaultFactsClient{recordingFactsClient: &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "done"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: window}, resolved: "measured"}}
			require.NoError(t, rt.RegisterModel("fake", provider))
			registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
				client, ok := in.Agent.ModelClient("fake")
				require.True(t, ok)
				_, err := client.Complete(ctx, &model.Request{Messages: in.Messages})
				if err != nil {
					return nil, err
				}
				return oracleFinal(), nil
			}})
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", []*model.Message{contentMessage("hello")}, WithRunID("root"))
			require.NoError(t, err)
			event, err := rt.continuationEvent(t.Context(), "root", hooks.ContinuationStarted)
			require.NoError(t, err)
			require.NotNil(t, event)
			assert.Equal(t, "fresh", event.Outcome)
			assert.Equal(t, "store_unsupported", event.Reason)
			published, err := rt.continuationEvent(t.Context(), "root", hooks.ContinuationPublished)
			require.NoError(t, err)
			assert.Nil(t, published)
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
			require.NoError(t, err)
			require.Len(t, record.Runs, 1)
			assert.True(t, record.Runs[0].Sealed)
			unknown := 0
			for _, event := range record.Runs[0].Events {
				if event.Type == hooks.ContextLimitsUnknown {
					unknown++
				}
			}
			if window == 0 {
				assert.Equal(t, 1, unknown)
			} else {
				assert.Zero(t, unknown)
			}
		})
	}
}
