package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	memoryinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/reminder"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerContextMemoryAndReminderLifecycle(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "without memory", true: "with memory"}[configured], func(t *testing.T) {
			store := memoryinmem.New()
			events := []memory.Event{
				{Type: memory.EventUserMessage, Timestamp: time.Unix(1, 0), Data: map[string]any{"text": "first"}},
				{Type: memory.EventUserMessage, Timestamp: time.Unix(2, 0), Data: map[string]any{"text": "latest"}},
				{Type: memory.EventAssistantMessage, Timestamp: time.Unix(3, 0), Data: map[string]any{"text": "answer"}},
			}
			require.NoError(t, store.AppendEvents(t.Context(), "content.agent", "root", events...))
			rt := New()
			if configured {
				rt.Memory = store
			} else {
				rt.Memory = nil
			}
			registerContentAgent(t, rt, &stubPlanner{
				start: func(_ context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
					assert.Equal(t, "content.agent", string(input.Agent.ID()))
					assert.Equal(t, "root", input.Agent.RunID())
					assert.NotNil(t, input.Agent.Logger())
					assert.NotNil(t, input.Agent.Metrics())
					assert.NotNil(t, input.Agent.Tracer())
					reader := input.Agent.Memory()
					users := reader.FilterByType(memory.EventUserMessage)
					latest, found := reader.Latest(memory.EventUserMessage)
					if configured {
						assert.Equal(t, events[:2], users)
						assert.True(t, found)
						assert.Equal(t, events[1], latest)
						users[0] = memory.Event{}
						assert.Equal(t, events[:2], reader.FilterByType(memory.EventUserMessage), "filtered slices must not alias the snapshot")
					} else {
						assert.Empty(t, users)
						assert.False(t, found)
					}
					_, found = reader.Latest(memory.EventType("absent"))
					assert.False(t, found)
					input.Agent.AddReminder(reminder.Reminder{ID: "keep", Text: "keep guidance"})
					input.Agent.AddReminder(reminder.Reminder{ID: "remove", Text: "obsolete guidance"})
					input.Agent.RemoveReminder("")
					input.Agent.RemoveReminder("remove")
					return oracleFinal(), nil
				},
				resume: func(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
					assert.Equal(t, []reminder.Reminder{{ID: "keep", Text: "keep guidance"}}, input.Reminders)
					input.Agent.RemoveReminder("keep")
					return oracleFinal(), nil
				},
			})
			input := &PlanActivityInput{AgentID: "content.agent", RunID: "root", RunContext: run.Context{RootRunID: "root", RunID: "root"}}
			_, err := rt.PlanStartActivity(t.Context(), input)
			require.NoError(t, err)
			_, err = rt.PlanResumeActivity(t.Context(), input)
			require.NoError(t, err)
			assert.Empty(t, rt.reminderSnapshot("root"))
			assert.Empty(t, rt.reminderSnapshot("other"))
		})
	}
}
