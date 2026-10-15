package transcript

import (
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLedgerDecodeRejectsCorruptPartsWithoutReplacingExistingHistory(t *testing.T) {
	for _, part := range []string{
		`true`, `123`, `{"Signature":true}`, `{"ToolUseID":42}`, `{"Name":false}`, `{"Text":false}`, `{"Citations":true}`,
	} {
		t.Run(part, func(t *testing.T) {
			ledger := NewLedger()
			ledger.AppendText("existing")
			before := ledger.BuildMessages()
			err := json.Unmarshal([]byte(`{"messages":[{"Role":"assistant","Parts":[`+part+`]}]}`), ledger)
			require.ErrorContains(t, err, "decode")
			assert.Equal(t, before, ledger.BuildMessages(), "a rejected restore must not replace existing history")
		})
	}
	for _, raw := range []string{`{"messages":true}`, `{"messages":[{"Role":42}]}`, `{"messages":[{"Parts":true}]}`} {
		ledger := NewLedger()
		require.Error(t, json.Unmarshal([]byte(raw), ledger))
		assert.True(t, ledger.IsEmpty())
	}
}

func TestLedgerEmptyResultsAndTextDoNotInventMessages(t *testing.T) {
	ledger := NewLedger()
	ledger.AppendText("")
	ledger.AppendUserToolResults(nil)
	ledger.AppendUserToolResults([]ToolResultSpec{{Content: "unidentified"}})
	assert.True(t, ledger.IsEmpty())
	require.NoError(t, json.Unmarshal([]byte(`{"messages":[{"Role":"user","Parts":[]}]}`), ledger))
	messages := ledger.BuildMessages()
	assert.Empty(t, messages)
}

func TestLedgerPreservesDistinctSignedThinkingBlocksInOrder(t *testing.T) {
	ledger := NewLedger()
	ledger.AppendThinking(ThinkingPart{Text: "first", Signature: "one", Index: 0, Final: true})
	ledger.AppendThinking(ThinkingPart{Text: "second", Signature: "two", Index: 1, Final: true})
	ledger.AppendThinking(ThinkingPart{Text: "third", Signature: "three", Index: 2, Final: true})
	ledger.FlushAssistant()
	messages := ledger.BuildMessages()
	require.Len(t, messages, 1)
	assert.Equal(t, []model.Part{model.ThinkingPart{Text: "first", Signature: "one", Index: 0, Final: true}, model.ThinkingPart{Text: "second", Signature: "two", Index: 1, Final: true}, model.ThinkingPart{Text: "third", Signature: "three", Index: 2, Final: true}}, messages[0].Parts)
}
