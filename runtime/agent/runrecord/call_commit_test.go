package runrecord_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallCommitKeysUnique(t *testing.T) {
	f := newRecordFixture()
	sequences := make(chan int, 256)
	failures := make(chan error, 256)
	var workers sync.WaitGroup
	for range 256 {
		workers.Go(func() {
			seq, err := f.store.NextSeq(context.Background(), "root", "run")
			if err != nil {
				failures <- err
				return
			}
			sequences <- seq
		})
	}
	workers.Wait()
	close(sequences)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	unique := make(map[int]bool)
	for seq := range sequences {
		assert.False(t, unique[seq])
		unique[seq] = true
	}
	assert.Len(t, unique, 256)
}

func TestCallCommitIndexMatchesAdoption(t *testing.T) {
	f := newRecordFixture()
	zombie := f.call(t, "run", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, "")
	winner := f.call(t, "run", 1, 2, runstore.CallCompleted, callrecord.ActivityPlanner, "")
	orphan := f.segment(t, "", []index.MessageEntry{{ID: 1, Kind: index.Assistant, Hash: f.put(t, []*model.Message{assistantMessage("zombie")}), Call: &zombie}})
	head := f.segment(t, "", []index.MessageEntry{{ID: 1, Kind: index.Assistant, Hash: f.put(t, []*model.Message{assistantMessage("winner")}), Call: &winner}})
	f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, []runstore.CallKey{winner}, recordedProtocolVersion, time.Unix(1, 0)))
	f.event(t, hooks.NewRunSealedEvent("run", head, recordedProtocolVersion, time.Unix(2, 0)))
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	assert.NotEqual(t, orphan, record.Runs[0].IndexHead)
	assert.Equal(t, &winner, record.Runs[0].Entries[0].Call)
	assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[1].Class)
	assert.Equal(t, runrecord.Abandoned, record.Runs[0].Calls[0].Class)
}

// Reader faults mutate durable evidence. Executable writer faults and structural
// retry prevention are covered by runtime/call_commit_writer_faults_test.go and
// runtime/call_commit_invariants_test.go.
func TestCallCommitFaultConfigurations(t *testing.T) {
	for _, fault := range []string{"adopt_by_entry", "presentation_without_key", "incomplete_as_abandoned", "index_first_stored"} {
		t.Run(fault, func(t *testing.T) {
			f := newRecordFixture()
			first := f.call(t, "run", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, "")
			auxiliary := f.call(t, "run", 1, 1, runstore.CallCompleted, callrecord.ActivityPlanner, "")
			adopted := []runstore.CallKey{first, auxiliary}
			switch fault {
			case "adopt_by_entry":
				adopted = adopted[:1]
				f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, adopted, recordedProtocolVersion, time.Unix(1, 0)))
				record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
				require.NoError(t, err)
				assert.NotEqual(t, runrecord.Adopted, record.Runs[0].Calls[1].Class, "ClassificationCorrect detects omitted adoption")
			case "incomplete_as_abandoned":
				open := f.call(t, "run", 1, 2, runstore.CallStarted, callrecord.ActivityPlanner, "")
				record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
				require.NoError(t, err)
				last := record.Runs[0].Calls[2]
				assert.Equal(t, open, last.Record.Key)
				assert.NotEqual(t, runrecord.Abandoned, last.Class, "incomplete-as-abandoned mutant disagrees with truth")
			case "presentation_without_key":
				f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, adopted, recordedProtocolVersion, time.Unix(1, 0)))
				commit := hooks.NewAssistantPresentationCommittedEvent("run", "agent", "", []string{"lost-key"}, nil)
				f.event(t, commit)
				record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
				require.NoError(t, err)
				assert.NotEqual(t, runrecord.PresentationAdopted, record.Runs[0].Presentations[0].Class, "PresentationClassified cannot infer a missing key from step")
			case "index_first_stored":
				winner := f.call(t, "run", 1, 2, runstore.CallCompleted, callrecord.ActivityPlanner, "")
				head := f.segment(t, "", []index.MessageEntry{{ID: 1, Kind: index.Assistant, Hash: f.put(t, []*model.Message{assistantMessage("wrong attempt")}), Call: &first}})
				f.event(t, hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, []runstore.CallKey{winner}, recordedProtocolVersion, time.Unix(1, 0)))
				f.event(t, hooks.NewRunSealedEvent("run", head, recordedProtocolVersion, time.Unix(2, 0)))
				record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
				require.NoError(t, err)
				assert.NotEqual(t, &winner, record.Runs[0].Entries[0].Call, "IndexMatchesAdoption detects first-stored attempt")
			default:
				t.Fatal(fmt.Sprintf("missing fault witness %s", fault))
			}
		})
	}
}

func assistantMessage(text string) *model.Message {
	return &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: text}}}
}
