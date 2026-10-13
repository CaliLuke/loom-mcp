package compaction

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// Clone detaches every pointer so callers cannot change submitted overrides.
func (o ContextOverrides) Clone() ContextOverrides {
	o.KeepRecentTokens = clonePointer(o.KeepRecentTokens)
	o.KeepUserTokens = clonePointer(o.KeepUserTokens)
	o.ProtectRecentToolTokens = clonePointer(o.ProtectRecentToolTokens)
	o.MinSavingTokens = clonePointer(o.MinSavingTokens)
	o.MaxSummaryTokens = clonePointer(o.MaxSummaryTokens)
	o.ReceiptTokens = clonePointer(o.ReceiptTokens)
	o.FramingTokens = clonePointer(o.FramingTokens)
	o.MaxSummarisedResultTokens = clonePointer(o.MaxSummarisedResultTokens)
	o.MaxPins = clonePointer(o.MaxPins)
	o.MaxPinnedParts = clonePointer(o.MaxPinnedParts)
	o.MaxSummaryChunks = clonePointer(o.MaxSummaryChunks)
	o.RecallDiscoveryCalls = clonePointer(o.RecallDiscoveryCalls)
	o.RecallReadCalls = clonePointer(o.RecallReadCalls)
	o.RecallScanBytes = clonePointer(o.RecallScanBytes)
	o.ContextWindow = clonePointer(o.ContextWindow)
	o.Threshold = clonePointer(o.Threshold)
	o.KeepNonTextResident = clonePointer(o.KeepNonTextResident)
	o.MaxTier = clonePointer(o.MaxTier)
	o.DisableRecall = clonePointer(o.DisableRecall)
	o.TriggerAt = clonePointer(o.TriggerAt)
	o.GuardMargin = clonePointer(o.GuardMargin)
	o.Summary = clonePointer(o.Summary)

	if o.ContextModel != nil {
		value := *o.ContextModel
		if value != nil {
			value = new(*value)
		}
		o.ContextModel = &value
	}
	return o
}

// UnmarshalJSON preserves an explicit null model as summaries-off, distinct
// from an omitted model which inherits the registered summariser.
func (o *ContextOverrides) UnmarshalJSON(data []byte) error {
	type wire ContextOverrides
	var value wire
	if err := json.Unmarshal(data, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, ok := fields["context_model"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var off *ContextModel
		value.ContextModel = &off
	}
	*o = ContextOverrides(value)
	return nil
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	return new(*value)
}
