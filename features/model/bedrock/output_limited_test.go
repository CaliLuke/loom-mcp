package bedrock

import (
	"testing"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
)

func TestBedrockOutputLimited(t *testing.T) {
	tests := []struct {
		name   string
		reason brtypes.StopReason
		want   bool
	}{
		{name: "maximum tokens", reason: brtypes.StopReasonMaxTokens, want: true},
		{name: "context window", reason: brtypes.StopReasonModelContextWindowExceeded, want: true},
		{name: "end turn", reason: brtypes.StopReasonEndTurn},
		{name: "tool use", reason: brtypes.StopReasonToolUse},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, bedrockOutputLimited(test.reason))
		})
	}
}
