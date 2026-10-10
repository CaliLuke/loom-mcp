package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	runstoreinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
)

func TestRunStoreConfiguration(t *testing.T) {
	custom := runstoreinmem.New()
	for _, tc := range []struct {
		name string
		rt   *Runtime
		want runstore.Store
	}{
		{"default", New(), nil},
		{"nil_option", New(WithRunStore(nil)), nil},
		{"option", New(WithRunStore(custom)), custom},
		{"options", newFromOptions(Options{RunStore: custom}), custom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotNil(t, tc.rt.RunStore)
			if tc.want != nil {
				assert.Same(t, tc.want, tc.rt.RunStore)
			} else {
				assert.IsType(t, &runstoreinmem.Store{}, tc.rt.RunStore)
			}
		})
	}
	first, second := New(), New()
	assert.NotSame(t, first.RunStore, second.RunStore)
}
