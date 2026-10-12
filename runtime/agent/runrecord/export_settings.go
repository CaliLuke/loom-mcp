package runrecord

import (
	"encoding/json/v2"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
)

// legacyExportSettings is the flattened ContextSettings wire shape written by
// K12-K16. Threshold-only compaction_settings uses its six untagged fields.
type legacyExportSettings struct {
	TriggerAt               contextmeasure.Rate
	KeepRecentTokens        int
	ProtectRecentToolTokens int
	MinSavingTokens         int
	RecallDiscoveryCalls    int
	RecallReadCalls         int
	Threshold               bool                     `json:"threshold,omitzero"`
	GuardMargin             contextmeasure.Rate      `json:"guard_margin,omitzero"`
	DisableRecall           bool                     `json:"disable_recall,omitzero"`
	Summary                 compaction.SummaryConfig `json:"summary,omitzero"`
}

// decodeExportSettings accepts exactly the current or historical settings shape
// at the export boundary. It preserves zero defaults and never invents a settings
// fingerprint for legacy records. Live settings decoding remains unchanged.
func decodeExportSettings(data []byte, settings *compaction.Settings) error {
	var current compaction.Settings
	currentErr := json.Unmarshal(data, &current, json.RejectUnknownMembers(true))
	if currentErr == nil {
		*settings = current
		return nil
	}
	var legacy legacyExportSettings
	if err := json.Unmarshal(data, &legacy, json.RejectUnknownMembers(true)); err != nil {
		return currentErr
	}
	*settings = compaction.Settings{
		TriggerAt:               legacy.TriggerAt,
		KeepRecentTokens:        legacy.KeepRecentTokens,
		ProtectRecentToolTokens: legacy.ProtectRecentToolTokens,
		MinSavingTokens:         legacy.MinSavingTokens,
		RecallDiscoveryCalls:    legacy.RecallDiscoveryCalls,
		RecallReadCalls:         legacy.RecallReadCalls,
		Threshold:               legacy.Threshold,
		GuardMargin:             legacy.GuardMargin,
		DisableRecall:           legacy.DisableRecall,
		Summary:                 legacy.Summary,
	}
	return nil
}
