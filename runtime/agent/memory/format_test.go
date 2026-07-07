package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFormatEntriesForPrompt(t *testing.T) {
	got := FormatEntriesForPrompt([]Entry{
		{
			Content:   " Remember scoped memory ",
			Author:    " user ",
			Timestamp: time.Date(2026, 7, 7, 12, 30, 0, 0, time.FixedZone("PDT", -7*60*60)),
		},
		{
			Content: "   ",
			Author:  "assistant",
		},
		{
			Content: "No timestamp",
		},
	})

	require.Equal(t, "<LONG_TERM_MEMORY>\nTime: 2026-07-07T19:30:00Z\nuser: Remember scoped memory\nNo timestamp\n</LONG_TERM_MEMORY>", got)
}

func TestFormatEntriesForPromptReturnsEmptyForNoContent(t *testing.T) {
	require.Empty(t, FormatEntriesForPrompt(nil))
	require.Empty(t, FormatEntriesForPrompt([]Entry{{Author: "user"}}))
}
