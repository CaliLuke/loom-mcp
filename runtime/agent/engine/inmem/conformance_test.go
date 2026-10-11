package inmem_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/enginetest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
)

func TestConformance(t *testing.T) {
	gaps := map[string]string{
		"await_signal": "E0 follow-up: Await requires a side-effect-free predicate, but Receiver offers only consuming ReceiveAsync and WorkflowContext has no signal handler/coroutine",
	}
	for _, kind := range []string{"hook", "planner", "tool", "finish", "record_content", "child_dispatch"} {
		gaps["activity_options/"+kind] = "E0 follow-up: inmem applies StartToCloseTimeout only; Queue, ScheduleToStartTimeout, HeartbeatTimeout and retry options are ignored"
	}
	enginetest.RunConformance(t, enginetest.Factory{
		Durable: false, Retries: false, KnownGaps: gaps,
		New: func(_ *testing.T) enginetest.Fixture {
			return enginetest.Fixture{Engine: inmem.New(), TaskQueue: "conformance", ActivityQueue: "activities"}
		},
	})
}
