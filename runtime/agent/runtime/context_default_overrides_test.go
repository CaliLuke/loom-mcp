package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextDefaultOverrideFieldwiseRecordedSettings(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		explicit, inheritedGuard            bool
		override                            ContextOverrides
		threshold, defaults, guard, invalid bool
	}{
		{name: "default inherits", inheritedGuard: true, defaults: true, guard: true},
		{name: "default becomes explicit guarded", inheritedGuard: true, override: ContextOverrides{Threshold: new(true)}, threshold: true, guard: true},
		{name: "explicit waiver", inheritedGuard: true, override: ContextOverrides{Threshold: new(true), KeepNonTextResident: new(false)}, threshold: true},
		{name: "default cannot waive", inheritedGuard: true, override: ContextOverrides{KeepNonTextResident: new(false)}, invalid: true},
		{name: "disable retains inert guard", inheritedGuard: true, override: ContextOverrides{Threshold: new(false)}, guard: true},
		{name: "disabled cannot waive", inheritedGuard: true, override: ContextOverrides{Threshold: new(false), KeepNonTextResident: new(false)}, invalid: true},
		{name: "explicit remains waived", explicit: true, override: ContextOverrides{Threshold: new(true)}, threshold: true},
		{name: "explicit inherits guard", explicit: true, inheritedGuard: true, override: ContextOverrides{Threshold: new(true)}, threshold: true, guard: true},
		{name: "inherited explicit permits waiver", explicit: true, inheritedGuard: true, override: ContextOverrides{KeepNonTextResident: new(false)}, threshold: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			reg := contextRegistration(rt)
			if tc.explicit {
				reg.Policy.ContextCompaction = &ContextCompaction{KeepNonTextResident: tc.inheritedGuard}
			}
			require.NoError(t, rt.RegisterAgent(t.Context(), reg))
			in := &RunInput{AgentID: reg.ID, RunID: "run", RootRunID: "run"}
			WithContextOverrides(tc.override)(in)
			// The run owns override pointers; caller mutation cannot change its snapshot.
			if tc.override.Threshold != nil {
				*tc.override.Threshold = !*tc.override.Threshold
			}
			if tc.override.KeepNonTextResident != nil {
				*tc.override.KeepNonTextResident = !*tc.override.KeepNonTextResident
			}
			rt.stampContextCompaction(in)
			err := rt.prepareContextStart(t.Context(), in)
			if tc.invalid {
				var setting *ContextSettingsError
				require.ErrorAs(t, err, &setting)
				assert.Equal(t, "KeepNonTextResident", setting.Field)
				assert.Empty(t, in.ContextFingerprint)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.threshold, in.ContextSettings.Threshold)
			assert.Equal(t, tc.defaults, in.ContextSettings.Default)
			assert.Equal(t, tc.guard, in.ContextSettings.KeepNonTextResident)
			data, err := rt.RunStore.Get(t.Context(), "run", runstore.Hash(in.ContextFingerprint))
			require.NoError(t, err)
			var recorded compaction.Settings
			require.NoError(t, compaction.DecodeCanonical(data, &recorded))
			assert.Equal(t, in.ContextSettings, recorded)
			assert.Equal(t, compaction.Canonical(in.ContextSettings), data)
			hash, err := rt.RunStore.Put(t.Context(), "run", compaction.Canonical(recorded))
			require.NoError(t, err)
			assert.Equal(t, in.ContextFingerprint, string(hash))
		})
	}
}
