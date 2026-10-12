// Package runrecord reconstructs durable run evidence without workflow history.
package runrecord

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// CallClass reports adoption independently of a call's terminal outcome.
	CallClass string
	// PresentationClass reports whether a committed presentation was adopted.
	PresentationClass string
	// RunRecord is a self-contained record of a run and its linked descendants.
	RunRecord struct {
		// RootRunID is the authorized content scope.
		RootRunID string `json:"root_run_id"`
		// Runs is parent-first, following canonical child-link order.
		Runs []Run `json:"runs"`
		// Content includes every resolved address, including server-only bytes.
		Content map[runstore.Hash][]byte `json:"content"`
	}
	// Run holds one run's transcript and evidence in canonical order.
	Run struct {
		// Summary rebuilds rejection disablement from canonical summary records.
		Summary compaction.SummaryState `json:"summary,omitzero"`
		// ContextFingerprint identifies the stored canonical effective settings.
		ContextFingerprint string `json:"context_fingerprint,omitzero"`
		// ContextSettings holds resolved recovery and threshold settings for every agent.
		ContextSettings *compaction.ContextSettings `json:"context_settings,omitzero"`
		// RecoverySettings is the resolved runtime-stamped lookup configuration.
		RecoverySettings *run.ContextRecovery `json:"recovery_settings,omitzero"`
		// Recall reconstructs independently charged and refilled allowances.
		Recall compaction.RecallAllowances `json:"recall,omitzero"`
		// CompactionSettings contains client-stamped threshold settings from adoption records.
		CompactionSettings *compaction.Settings `json:"compaction_settings,omitzero"`
		// KeepResultTools contains the recorded result retention list.
		KeepResultTools []tools.Ident `json:"keep_result_tools,omitzero"`
		// RunID identifies this run.
		RunID string `json:"run_id"`
		// WorkflowProtocolVersion is the record-event stamp, or zero when no stamp exists.
		WorkflowProtocolVersion int `json:"workflow_protocol_version"`
		// Sealed reports whether terminal workflow code committed the last head.
		Sealed bool `json:"sealed"`
		// IndexHead is the seal head, or the latest planner-step head.
		IndexHead runstore.Hash `json:"index_head"`
		// Entries contains only the authoritative head's reachable entries.
		Entries []index.MessageEntry `json:"entries"`
		// Transcript is the rendered transcript, without history-policy filtering.
		Transcript []*model.Message `json:"transcript"`
		// Checkpoints contains every checkpoint resolved from recorded views.
		Checkpoints []Checkpoint `json:"checkpoints"`
		// Calls includes every attempt, including zombie and incomplete records.
		Calls []Call `json:"calls"`
		// Presentations describes committed presentations independently of transcript.
		Presentations []Presentation `json:"presentations"`
		// TypedAnswers contains complete typed-input payloads.
		TypedAnswers []ContentPart `json:"typed_answers"`
		// AwaitAnswers contains complete clarification answers.
		AwaitAnswers []ContentPart `json:"await_answers"`
		// Results contains full model-eligible results, including previewed results.
		Results []ContentPart `json:"results"`
		// ServerOnly contains record-only content, never included in Transcript.
		ServerOnly []ContentPart `json:"server_only"`
		// Events is the canonical run log, in store order.
		Events []*runlog.Event `json:"events"`
	}
	// Call combines the original evidence with its exact rebuilt request.
	Call struct {
		// Record is the original opaque keyed call record.
		Record runstore.CallRecord `json:"record"`
		// Class is derived solely from canonical adoption events and record state.
		Class CallClass `json:"class"`
		// Activity identifies the activity that made the call.
		Activity callrecord.ActivityKind `json:"activity"`
		// Step is the activity's recorded planner step.
		Step int `json:"step"`
		// Started is the decoded immutable request and identity.
		Started callrecord.Started `json:"started"`
		// Completion is nil for incomplete calls.
		Completion *callrecord.Completion `json:"completion"`
		// Request is the verified exported model request.
		Request *model.Request `json:"request"`
	}
	// Presentation is one committed presentation and its adoption classification.
	Presentation struct {
		// ID is the activity's presentation identifier.
		ID string `json:"id"`
		// EventKey identifies the canonical atomic presentation commit.
		EventKey string `json:"event_key"`
		// Call names its recorded model invocation, or is nil for standalone streams.
		Call *runstore.CallKey `json:"call"`
		// Class separates adopted, unadopted and unrecorded presentations.
		Class PresentationClass `json:"class"`
	}
	// ContentPart associates full content with its transcript entry or run-log event.
	ContentPart struct {
		// EntryID is the originating canonical transcript position.
		EntryID index.MessageID `json:"entry_id"`
		// InputID identifies a typed input, when applicable.
		InputID string `json:"input_id,omitzero"`
		// Hash is the original content address.
		Hash runstore.Hash `json:"hash"`
		// Content is the exact stored payload, not a preview.
		Content []byte `json:"content"`
	}
	// Checkpoint qualifies resolved compaction evidence by content scope.
	Checkpoint struct {
		// Scope owns the immutable record.
		Scope string `json:"scope"`
		// Hash is its content address.
		Hash runstore.Hash `json:"hash"`
		// Record contains every field of the stored checkpoint.
		Record index.CheckpointRecord `json:"record"`
	}
)

const (
	// Adopted means a canonical record event names this call.
	Adopted CallClass = "adopted"
	// Abandoned means a terminal call was not named by a record event.
	Abandoned CallClass = "abandoned"
	// Incomplete means a call started without a durable completion.
	Incomplete CallClass = "incomplete"
	// PresentationAdopted means the presentation's call is adopted.
	PresentationAdopted PresentationClass = "adopted"
	// PresentedNotAdopted means a recorded presentation's call was not adopted.
	PresentedNotAdopted PresentationClass = "presented-not-adopted"
	// PresentedUnrecorded means a standalone presentation carries no call key.
	PresentedUnrecorded PresentationClass = "presented-unrecorded"
)
