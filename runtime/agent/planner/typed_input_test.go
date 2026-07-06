package planner

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/stretchr/testify/require"
)

func TestAwaitTypedInputItem(t *testing.T) {
	item := AwaitTypedInputItem(&AwaitTypedInput{
		ID:     "approval",
		Title:  "Approval",
		Schema: rawjson.Message([]byte(`{"type":"object"}`)),
	})

	require.Equal(t, AwaitItemKindTypedInput, item.Kind)
	require.Equal(t, "approval", item.TypedInput.ID)
}

func TestPlanResumeInputCarriesTypedInputs(t *testing.T) {
	in := &PlanResumeInput{
		TypedInputs: []TypedInputOutput{{ID: "approval", Payload: rawjson.Message([]byte(`{"approved":true}`))}},
	}
	require.Equal(t, "approval", in.TypedInputs[0].ID)
}
