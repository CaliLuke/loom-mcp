package suite_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cOverlayOnlyFillsRequestedMissingKeys(t *testing.T) {
	for _, tc := range []struct {
		name              string
		requested, actual int
		validParent       bool
		wantError         bool
	}{
		{"missing repeat", 2, 2, false, false},
		{"unrequested child repeat", 2, 1, false, true},
		{"valid observation replacement", 2, 2, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storeDir := t.TempDir()
			store, err := eval.Open(storeDir)
			require.NoError(t, err)
			freeze := gate.Freeze{Format: gate.FreezeFormat, Version: 2, Configuration: "offline", Policy: suite.ProposedOfflinePolicy()}
			label, err := freeze.Label(1)
			require.NoError(t, err)
			cfg := eval.Config{Harness: func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
				panic("store-only fixture")
			}, Variants: []eval.Variant{{ID: "receipt_only"}}, Repeats: 2, Scenarios: []eval.Scenario{{ID: "s", Agent: "agent", Items: eval.Items(eval.Item{ID: "i", Input: "hello"}), Expect: []expect.Expectation{expect.Completed()}}}}
			parent, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Label: label, Identity: &eval.Identity{}})
			require.NoError(t, err)
			key := eval.AttemptKey{Variant: "receipt_only", Scenario: "s", Item: "i", Repeat: 1}
			require.NoError(t, parent.Append(&eval.AttemptRecord{Index: 0, AttemptKey: key, Outcome: &eval.Outcome{}}))
			if tc.validParent {
				key.Repeat = 2
				require.NoError(t, parent.Append(&eval.AttemptRecord{Index: 1, AttemptKey: key, Outcome: &eval.Outcome{}}))
			}
			key.Repeat = tc.requested
			cfg.Select.Keys = []eval.AttemptKey{key}
			child, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Label: label, Parent: parent.ID(), Identity: &eval.Identity{}})
			require.NoError(t, err)
			key.Repeat = tc.requested
			require.NoError(t, child.Append(&eval.AttemptRecord{Index: 0, AttemptKey: key, Outcome: &eval.Outcome{}}))
			if tc.actual != tc.requested {
				confined, err := os.OpenRoot(storeDir)
				require.NoError(t, err)
				data, err := confined.ReadFile(filepath.Join(child.ID(), "attempts.jsonl"))
				require.NoError(t, err)
				changed := strings.ReplaceAll(string(data), `"repeat":2`, `"repeat":1`)
				changed = strings.ReplaceAll(changed, "receipt_only:s:i:2", "receipt_only:s:i:1")
				require.NotEqual(t, string(data), changed)
				require.NoError(t, confined.WriteFile(filepath.Join(child.ID(), "attempts.jsonl"), []byte(changed), 0600))
				require.NoError(t, confined.Close())
			}
			rows, err := suite.Overlay(store, child.ID(), freeze, 1)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, rows, 2)
			assert.Equal(t, 1, rows[0].Repeat)
			assert.Equal(t, 2, rows[1].Repeat)
		})
	}
}
