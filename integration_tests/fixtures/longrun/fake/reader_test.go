package fake_test

import (
	"context"
	"testing"

	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReaderNeedsEvidenceAndFaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		faults fake.Faults
		want   string
	}{
		{"received", fake.Faults{}, "TAR-12345"},
		{"fabricated", fake.Faults{Fabricate: true}, "BAD-99999"},
		{"prefer_decoy", fake.Faults{PreferDecoy: true}, "OLD-12345"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fake.Reader{Faults: tc.faults}
			req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "target = OLD-12345\ntarget = TAR-12345\nNEED:{\"tool\":\"ops_deployer.deploy\",\"step\":\"final\",\"keys\":[\"target\"]}"}}}}}
			got, err := p.Complete(context.Background(), req)
			require.NoError(t, err)
			require.Len(t, got.ToolCalls, 1)
			assert.Contains(t, string(got.ToolCalls[0].Payload), tc.want)
			assert.Positive(t, got.Usage.InputTokens)
		})
	}
}
