package agenttest

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forbiddenCaptureClient must never reach a provider during construction.
type forbiddenCaptureClient struct {
	t *testing.T
}

// Non-comparable clients and capabilities are valid model implementations.
type nonComparableCaptureClient struct {
	model.Client
	_ []int
}

type nonComparableCounter struct {
	model.TokenCounter
	_ []int
}

type nonComparableContextInfo struct {
	model.ContextInfo
	_ []int
}

func TestHarnessRejectsModelChangedBeforeSeal(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		override bool
	}{
		{name: "existing", id: "existing"},
		{name: "added", id: "added"},
		{name: "existing with limits", id: "existing", override: true},
		{name: "added with limits", id: "added", override: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &forbiddenCaptureClient{t: t}
			var registered *runtime.Runtime
			var opts []Option
			if tc.override {
				opts = append(opts, WithRuntimeOptions(runtime.WithContextLimits(map[string]model.ContextLimits{"existing": {ContextWindow: 8192}})))
			}
			h, err := newHarness(t.Context(), func(_ context.Context, rt *runtime.Runtime) error {
				registered = rt
				return rt.RegisterModel("existing", client)
			}, func(rt *runtime.Runtime) {
				require.NoError(t, rt.RegisterModel(tc.id, client))
			}, opts...)
			if h != nil {
				require.NoError(t, h.Close())
			}
			assert.Nil(t, h)
			require.ErrorContains(t, err, "capture gate changed during registration")
			require.ErrorContains(t, err, tc.id)
			assert.ErrorIs(t, registered.RegisterModel("late", client), runtime.ErrRegistrationClosed)
		})
	}
}

func TestHarnessGateIdentityPreservesCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		counter, info, override bool
	}{
		{name: "plain"},
		{name: "counter", counter: true},
		{name: "info", info: true},
		{name: "both", counter: true, info: true},
		{name: "runtime limits", counter: true, override: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var counter model.TokenCounter
			if tc.counter {
				counter = nonComparableCounter{TokenCounter: model.TokenEstimator{}}
			}
			var info model.ContextInfo
			if tc.info {
				script, err := NewScript()
				require.NoError(t, err)
				info = nonComparableContextInfo{ContextInfo: script.WithContextLimits(model.ContextLimits{ContextWindow: 4096})}
			}
			client := model.WithCapabilities(nonComparableCaptureClient{Client: &forbiddenCaptureClient{t: t}}, counter, info)
			var opts []Option
			if tc.override {
				opts = append(opts, WithRuntimeOptions(runtime.WithContextLimits(map[string]model.ContextLimits{"model": {ContextWindow: 8192}})))
			}
			h, err := NewHarness(t.Context(), func(_ context.Context, rt *runtime.Runtime) error {
				return rt.RegisterModel("model", client)
			}, opts...)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, h.Close()) })
			gated, ok := h.Runtime().ModelClient("model")
			require.True(t, ok)
			_, hasCounter := gated.(model.TokenCounter)
			limits, hasInfo := gated.(model.ContextInfo)
			assert.Equal(t, tc.counter, hasCounter)
			assert.Equal(t, tc.info || tc.override, hasInfo)
			if hasInfo {
				got, _, known := limits.ContextLimits(t.Context(), &model.Request{Model: "model"})
				assert.True(t, known)
				want := 4096
				if tc.override {
					want = 8192
				}
				assert.Equal(t, want, got.ContextWindow)
			}
		})
	}
}

func (c *forbiddenCaptureClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	c.t.Error("ungated Complete invoked")
	return nil, nil
}

func (c *forbiddenCaptureClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	c.t.Error("ungated Stream invoked")
	return nil, nil
}
