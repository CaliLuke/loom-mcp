package runtime

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recoveryPathStore injects a failure only at the raw correction write, after
// call recording has stored its JSON request and response evidence.
type recoveryPathStore struct {
	*contentPathStore
	failCorrection bool
}

func TestContentRecoveryCorrectionStoredBeforeReturn(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, failAt := range []int{0, 1} {
			t.Run(fmt.Sprintf("resume=%v/fail=%d", resume, failAt), func(t *testing.T) {
				store := &recoveryPathStore{contentPathStore: &contentPathStore{Store: storeinmem.New()}, failCorrection: failAt > 0}
				rt := New(WithRunStore(store))
				rt.models["default"] = &activityRecoveryUsageClient{rejected: restoredValidationError(t, model.OutputValidationToolArguments)}
				registerContentAgent(t, rt, activityRecoveryUsagePlanner{})
				out, err := executeContentPlannerActivity(t, rt, &api.PlanActivityInput{AgentID: "content.agent", RunID: "child", RunContext: run.Context{RunID: "child", RootRunID: "root", Attempt: 1}}, resume)
				if failAt > 0 {
					require.ErrorIs(t, err, runstore.ErrContentTooLarge)
					assert.Nil(t, out)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, out.Recovery)
				require.NotEmpty(t, out.Recovery.CorrectionRef)
				var writes []contentPathWrite
				for _, write := range store.snapshot() {
					if write.hash == out.Recovery.CorrectionRef {
						writes = append(writes, write)
					}
				}
				require.Len(t, writes, 1)
				assert.Equal(t, "root", writes[0].scope)
				assert.Empty(t, out.Recovery.Correction)
				assert.NotEmpty(t, writes[0].data)
				assert.Equal(t, writes[0].hash, out.Recovery.CorrectionRef)
			})
		}
	}
}

func (s *recoveryPathStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	if s.failCorrection && !jsontext.Value(data).IsValid() {
		return "", runstore.ErrContentTooLarge
	}
	return s.contentPathStore.Put(ctx, scope, data)
}
