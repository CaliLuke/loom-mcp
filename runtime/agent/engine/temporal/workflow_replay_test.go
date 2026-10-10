package temporal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
)

func TestWorkflowReplayerRequiresKnownDataConverter(t *testing.T) {
	for _, test := range []struct {
		name      string
		external  bool
		options   *client.Options
		wantError bool
	}{
		{name: "owned default", options: &client.Options{}},
		{name: "owned custom", options: &client.Options{DataConverter: NewAgentDataConverter(nil)}},
		{name: "external explicit default", external: true, options: &client.Options{}},
		{name: "external custom", external: true, options: &client.Options{DataConverter: NewAgentDataConverter(nil)}},
		{name: "external unknown", external: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := Options{ClientOptions: test.options, WorkerOptions: WorkerOptions{TaskQueue: "replay"}}
			if test.external {
				opts.Client = newLazyTestClient(t)
			}
			eng, err := NewWorker(opts)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, eng.Close())
			})
			err = eng.ReplayWorkflowHistory(nil, &historypb.History{})
			if test.wantError {
				assert.ErrorContains(t, err, "ClientOptions")
				return
			}
			// The SDK must receive the history once converter configuration is known.
			assert.ErrorContains(t, err, "empty events")
		})
	}
}
