package runtime

import (
	"encoding/json/v2"
	"fmt"
	"reflect"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextOverrideStartValidation(t *testing.T) {
	type validationCase struct {
		name      string
		overrides ContextOverrides
		field     string
	}
	cases := []validationCase{
		{"trigger one", ContextOverrides{TriggerAt: new(Ratio{Num: 1, Den: 1})}, "TriggerAt"},
		{"model", ContextOverrides{ContextModel: new(new(ContextModel{ID: "missing"}))}, "ContextModel"},
		{"tier", ContextOverrides{MaxTier: new(compaction.Clear)}, "MaxTier"},
		{"trigger zero", ContextOverrides{TriggerAt: new(Ratio{Num: 0, Den: 1})}, "TriggerAt"},
		{"trigger denominator", ContextOverrides{TriggerAt: new(Ratio{Num: 1, Den: 0})}, "TriggerAt"},
		{"margin over", ContextOverrides{GuardMargin: new(Ratio{Num: 51, Den: 100})}, "GuardMargin"},
		{"margin negative", ContextOverrides{GuardMargin: new(Ratio{Num: -1, Den: 10})}, "GuardMargin"},
		{"margin denominator", ContextOverrides{GuardMargin: new(Ratio{Num: 0, Den: 0})}, "GuardMargin"},
		{"chunks", ContextOverrides{MaxSummaryChunks: new(65)}, "MaxSummaryChunks"},
		{"tier invalid", ContextOverrides{Threshold: new(true), MaxTier: new(compaction.Tier("invalid"))}, "MaxTier"},
	}
	typ := reflect.TypeFor[ContextOverrides]()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type != reflect.TypeFor[*int]() {
			continue
		}
		for _, value := range []int{0, -1} {
			var overrides ContextOverrides
			reflect.ValueOf(&overrides).Elem().Field(i).Set(reflect.ValueOf(new(value)))
			cases = append(cases, validationCase{fmt.Sprintf("%s=%d", typ.Field(i).Name, value), overrides, typ.Field(i).Name})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := &rejectingStartEngine{err: engine.ErrWorkflowStartRejected}
			rt := New(WithEngine(eng))
			in := &RunInput{AgentID: "agent", RunID: "run"}
			WithContextOverrides(tc.overrides)(in)
			_, err := rt.startOneShotRunWithRoute(t.Context(), in, AgentRoute{ID: "agent", WorkflowName: "agent.workflow", DefaultTaskQueue: "q"})
			var setting *ContextSettingsError
			require.ErrorAs(t, err, &setting)
			assert.Equal(t, tc.field, setting.Field)
			assert.Zero(t, eng.calls)
		})
	}
}

func TestContextOverrideFingerprintStart(t *testing.T) {
	rt := New(WithContextCompaction("agent", ContextCompaction{}))
	require.NoError(t, rt.RegisterModel("summary", contextLimitsClient{}))
	fingerprints := make([]string, 0, 5)
	for _, overrides := range []ContextOverrides{
		{}, {ContextWindow: new(8000)}, {ContextWindow: new(9000)},
		{ContextModel: new(new(ContextModel{ID: "summary"}))},
		{ContextModel: new(new(ContextModel{ID: "summary", Class: model.ModelClass("small")}))},
	} {
		in := &RunInput{AgentID: "agent", RunID: "run", RootRunID: "run"}
		WithContextOverrides(overrides)(in)
		rt.stampContextCompaction(in)
		require.NoError(t, rt.prepareContextStart(t.Context(), in))
		require.NotEmpty(t, in.ContextFingerprint)
		data, err := rt.RunStore.Get(t.Context(), "run", runstore.Hash(in.ContextFingerprint))
		require.NoError(t, err)
		var settings compaction.Settings
		require.NoError(t, compaction.DecodeCanonical(data, &settings))
		assert.Equal(t, in.ContextSettings, settings)
		assert.NotContains(t, fingerprints, in.ContextFingerprint)
		fingerprints = append(fingerprints, in.ContextFingerprint)
	}
}

func TestContextOverrideWire(t *testing.T) {
	var off *ContextModel
	original := api.PolicyOverrides{Context: &ContextOverrides{ContextModel: &off, GuardMargin: new(Ratio{Num: 0, Den: 1})}}
	data, err := json.Marshal(original)
	require.NoError(t, err)
	var decoded api.PolicyOverrides
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotNil(t, decoded.Context)
	require.NotNil(t, decoded.Context.ContextModel, "explicit summaries off must survive wire null")
	assert.Nil(t, *decoded.Context.ContextModel)
	require.NoError(t, json.Unmarshal([]byte(`{}`), &decoded))
	assert.Nil(t, decoded.Context)
}

func TestContextOverrideStartBoundaries(t *testing.T) {
	for _, overrides := range []ContextOverrides{
		{TriggerAt: new(Ratio{Num: 99, Den: 100})}, {GuardMargin: new(Ratio{Num: 0, Den: 1})}, {GuardMargin: new(Ratio{Num: 1, Den: 2})}, {MaxSummaryChunks: new(64)},
	} {
		eng := &rejectingStartEngine{err: engine.ErrWorkflowStartRejected}
		rt := New(WithEngine(eng))
		in := &RunInput{AgentID: "agent", RunID: "run"}
		WithContextOverrides(overrides)(in)
		_, err := rt.startOneShotRunWithRoute(t.Context(), in, AgentRoute{ID: "agent", WorkflowName: "workflow", DefaultTaskQueue: "queue"})
		assert.Equal(t, 1, eng.calls)
		var invalid *ContextSettingsError
		assert.NotErrorAs(t, err, &invalid)
	}
}
