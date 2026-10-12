package inmem

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/enginetest"
)

func TestConformance(t *testing.T) {
	enginetest.RunConformance(t, enginetest.Factory{
		Durable: false, Retries: false,
		New: func(_ *testing.T) enginetest.Fixture {
			var mu sync.Mutex
			recorded := make(map[[2]string][]engine.ActivityOptions)
			e := New().(*eng)
			e.observeActivity = func(workflowID, name string, opts engine.ActivityOptions) {
				mu.Lock()
				defer mu.Unlock()
				key := [2]string{workflowID, name}
				recorded[key] = append(recorded[key], opts)
			}
			return enginetest.Fixture{
				Engine: e, TaskQueue: "conformance", ActivityQueue: "activities",
				VerifyActivityOptions: func(t *testing.T, workflowID, name string, want []engine.ActivityOptions) error {
					t.Helper()
					mu.Lock()
					defer mu.Unlock()
					got := recorded[[2]string{workflowID, name}]
					if !assert.ObjectsAreEqual(want, got) {
						return &enginetest.GapError{Err: fmt.Errorf("scheduled activity options: want %#v, got %#v", want, got)}
					}
					return nil
				},
			}
		},
	})
}
