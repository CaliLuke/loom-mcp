package anthropic

import (
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
)

func TestAnthropicOutputLimited(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   bool
	}{
		{name: "maximum tokens", reason: string(sdk.StopReasonMaxTokens), want: true},
		{name: "end turn", reason: string(sdk.StopReasonEndTurn)},
		{name: "tool use", reason: string(sdk.StopReasonToolUse)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, anthropicOutputLimited(test.reason))
		})
	}
}
