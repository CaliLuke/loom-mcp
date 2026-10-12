package suite_test

import (
	"testing"

	"example.com/longrun/fake"
	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDryGate(t *testing.T) {
	dir := ".."
	for _, tc := range []struct {
		name   string
		faults fake.Faults
		want   gate.Status
	}{
		{"reader", fake.Faults{}, gate.Pass},
		{"fault", fake.Faults{DropFacts: true, NoRecall: true}, gate.Fail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := suite.DryGate(t.Context(), dir, tc.faults, 4)
			require.NoError(t, err)
			require.NotEmpty(t, result.Decisions)
			t.Logf("decision: %+v; evaluation: %+v", result.Decisions, result.Evaluations)
			assert.Equal(t, tc.want, result.Decisions[len(result.Decisions)-1].Status)
			if tc.want == gate.Fail {
				assert.Equal(t, 1, result.Decisions[0].Look)
				assert.Contains(t, result.Decisions[0].Reason, "success")
			}
		})
	}
}
