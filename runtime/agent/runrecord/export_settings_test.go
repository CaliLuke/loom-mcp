package runrecord_test

import (
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportPreK17ContextSettings(t *testing.T) {
	// Exact flattened wire names from K12-K16, before Settings became the
	// ContextSettings alias. Nonzero values make loss of any setting observable.
	const settings = `"TriggerAt":{"Tokens":85,"Bytes":100},"KeepRecentTokens":1000,"ProtectRecentToolTokens":500,"MinSavingTokens":250,"RecallDiscoveryCalls":7,"RecallReadCalls":9`
	for _, protocol := range []int{9, 13, 14} {
		t.Run(fmt.Sprint(protocol), func(t *testing.T) {
			summary := ""
			wantSummary := compaction.SummaryConfig{}
			if protocol >= 13 {
				summary = `,"summary":{"Enabled":true,"Model":{"ID":"summarizer","Class":"small","AcceptsImages":true,"AcceptsDocuments":true},"Settings":{"Instructions":"retain facts","Prompt":"custom"}}`
				wantSummary = compaction.SummaryConfig{Enabled: true, Model: compaction.ContextModel{ID: "summarizer", Class: "small", AcceptsImages: true, AcceptsDocuments: true}, Settings: compaction.SummarySettings{Instructions: "retain facts", Prompt: "custom"}}
			}
			data := fmt.Sprintf(`{"version":1,"root_run_id":"run","runs":[{"run_id":"run","workflow_protocol_version":%d,"context_settings":{%s,"threshold":true,"guard_margin":{"Tokens":1,"Bytes":10},"disable_recall":true%s},"compaction_settings":{%s}}],"content":{}}`, protocol, settings, summary, settings)
			record, err := runrecord.FromExport([]byte(data))
			require.NoError(t, err)
			require.Len(t, record.Runs, 1)
			run := record.Runs[0]
			want := compaction.Settings{TriggerAt: contextmeasure.Rate{Tokens: 85, Bytes: 100}, KeepRecentTokens: 1000, ProtectRecentToolTokens: 500, MinSavingTokens: 250, RecallDiscoveryCalls: 7, RecallReadCalls: 9}
			assert.Equal(t, &want, run.CompactionSettings)
			want.Threshold = true
			want.GuardMargin = contextmeasure.Rate{Tokens: 1, Bytes: 10}
			want.DisableRecall = true
			want.Summary = wantSummary
			assert.Equal(t, &want, run.ContextSettings)
			assert.Empty(t, run.ContextFingerprint)
			again, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(again)
			require.NoError(t, err)
			assert.Equal(t, record, restored)
		})
	}
}

func TestExportContextSettingsStrictness(t *testing.T) {
	for _, test := range []struct {
		name, settings string
		valid          bool
	}{
		{"current", `{"keep_recent_tokens":1000,"context_window":6000}`, true},
		{"absent", `null`, true},
		{"unknown current", `{"keep_recent_tokens":1000,"unexpected":true}`, false},
		{"unknown legacy", `{"KeepRecentTokens":1000,"unexpected":true}`, false},
		{"mixed shapes", `{"KeepRecentTokens":1000,"context_window":6000}`, false},
		{"unknown nested", `{"summary":{"unexpected":true}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := fmt.Sprintf(`{"version":1,"root_run_id":"run","runs":[{"run_id":"run","context_fingerprint":"current","context_settings":%s}],"content":{}}`, test.settings)
			record, err := runrecord.FromExport([]byte(data))
			if !test.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "current", record.Runs[0].ContextFingerprint)
			if test.name == "current" {
				assert.Equal(t, &compaction.Settings{KeepRecentTokens: 1000, ContextWindow: 6000}, record.Runs[0].ContextSettings)
			} else {
				assert.Nil(t, record.Runs[0].ContextSettings)
			}
		})
	}
}
