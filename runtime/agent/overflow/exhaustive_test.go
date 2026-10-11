package overflow_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/overflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type sentRequest struct {
	size     int
	overflow bool
}

type guardState struct {
	view, initial, fixed                   int
	projection                             index.ViewRef
	planner                                string
	calibrated, exact, known               bool
	estimateError                          int
	step                                   overflow.Step
	lastRejected, guardRejections, actions int
	sent                                   []sentRequest
	status, failure                        string
	failRequest                            int
}

const (
	window        = 2
	unit          = 4096
	maxView       = 6
	planning      = "planning"
	success       = "ok"
	failed        = "failed"
	noProgress    = "no_progress"
	budgetFailure = "budget"
	passes        = "passes"
	unrelated     = "unrelated"
	noFault       = "none"
	guardFails    = "guard_fails_step"
	compareLast   = "compare_last_rejection"
	compareView   = "compare_view_only"
	guardSpends   = "guard_spends_overflow_budget"
	guardNeverOff = "guard_never_off"
)

// The formal view/window units are scaled together so B-derived checkpoints
// remain renderable. Both zero through two units of outside-view overhead are
// enumerated; Recover and Check are the production implementations. The fake
// provider charges the conservative result-projection bounds used by Recover.
func TestCheckOverflowGuardStateSpace(t *testing.T) {
	violations, witness, traces := exploreGuard(noFault)
	assert.Empty(t, violations)
	assert.True(t, witness, "WitnessGuardThenProvider")
	assert.GreaterOrEqual(t, traces, 1296, "all formal initial states must terminate")
	t.Logf("1296 initial states, %d terminal calibration-branch traces", traces)
}

func TestCheckOverflowGuardFaultMutants(t *testing.T) {
	for _, tc := range []struct {
		fault, invariant string
	}{
		{guardFails, "FailsOnlyOnProvider"},
		{compareLast, "FailsOnlyOnProvider"},
		{compareView, "RetriesShrink"},
		{guardSpends, "BudgetFailureHonest"},
		{guardNeverOff, "Terminates"},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			violations, _, traces := exploreGuard(tc.fault)
			assert.Contains(t, violations, tc.invariant)
			assert.Positive(t, traces)
		})
	}
}

func exploreGuard(fault string) (map[string]string, bool, int) {
	violations := make(map[string]string)
	witness, traces := false, 0
	for _, initial := range initialGuardStates() {
		queue := []guardState{initial}
		for len(queue) > 0 {
			state := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			for name, holds := range guardInvariants(state) {
				if !holds {
					violations[name] = fmt.Sprintf("%+v", state)
				}
			}
			if state.status != planning {
				traces++
				witness = witness || (state.status == success && state.guardRejections == 1 && overflowCount(state.sent) > 0)
				continue
			}
			if state.actions >= 2*maxView+4 {
				violations["Terminates"] = fmt.Sprintf("%+v", state)
				traces++
				continue
			}
			queue = append(queue, advanceGuard(state, fault)...)
		}
	}
	return violations, witness, traces
}

func initialGuardStates() []guardState {
	states := make([]guardState, 0, 1296)
	for view := 1; view <= maxView; view++ {
		for _, planner := range []string{passes, "rewrites", unrelated} {
			for flags := range 8 {
				for estimateError := -1; estimateError <= 1; estimateError++ {
					for fixed := 0; fixed <= 2; fixed++ {
						states = append(states, guardState{view: 3 * unit * view, initial: 3 * unit * view, fixed: 3 * unit * fixed, planner: planner,
							calibrated: flags&1 != 0, exact: flags&2 != 0, known: flags&4 != 0,
							estimateError: estimateError, status: planning, step: overflow.Step{ProviderLeft: 2}})
					}
				}
			}
		}
	}
	return states
}

func advanceGuard(state guardState, fault string) []guardState {
	state.actions++
	reqBytes := state.initial
	if state.planner == passes {
		reqBytes = state.view
	}
	facts := contextmeasure.RequestFacts{Model: "m", TextBytes: reqBytes + state.fixed, ViewTextBytes: reqBytes, ViewBearing: state.planner != unrelated}
	if state.known {
		facts.ContextWindow = window * unit
	}
	req := overflow.Size(facts)
	estimate := req
	if !state.exact {
		estimate += state.estimateError * unit
	}
	step := state.step
	switch fault {
	case compareView:
		step.Rejected = nil
	case compareLast:
		if state.lastRejected > 0 {
			step.Rejected = map[string]int{"m": state.lastRejected}
		}
	case guardNeverOff:
		step.GuardUsed, step.GuardOff = false, false
	}
	action := overflow.Check(step, facts, contextmeasure.GuardCount{Tokens: estimate, GuardOn: state.known && (state.exact || state.calibrated)}, overflow.Limits{Budget: window * unit})
	switch action {
	case overflow.GuardReject:
		return []guardState{guardRejected(state, req, fault)}
	case overflow.NoProgress:
		state.status, state.failure, state.failRequest = failed, noProgress, req
		return []guardState{state}
	case overflow.Send:
		return providerSent(state, req)
	}
	panic("unknown Check action")
}

func guardRejected(state guardState, req int, fault string) guardState {
	state.guardRejections++
	state.lastRejected = req
	state.step.GuardUsed = fault != guardNeverOff
	if fault == guardSpends && state.step.ProviderLeft > 0 {
		state.step.ProviderLeft--
	}
	switch {
	case state.planner != unrelated && state.view > 1:
		state = recoveredGuardView(state, req)
	case fault == guardFails:
		state.status, state.failure, state.failRequest = failed, "guard", req
	default:
		state.step.GuardOff = fault != guardNeverOff
	}
	return state
}

func providerSent(state guardState, req int) []guardState {
	state.sent = append(append([]sentRequest{}, state.sent...), sentRequest{size: req, overflow: req > window*unit})
	if req <= window*unit {
		state.status = success
		return []guardState{state}
	}
	state.lastRejected = req
	state.failRequest = req
	switch {
	case state.step.ProviderLeft == 0:
		state.status, state.failure = failed, budgetFailure
	case state.planner == unrelated:
		state.status, state.failure = failed, "not_view_bearing"
	case compaction.Minimal(guardEntries(state), state.projection):
		state.status, state.failure = failed, "minimal"
	default:
		state.step.ProviderLeft--
		baseline, exists := state.step.Rejected["m"]
		if !exists || req < baseline {
			state.step.Rejected = map[string]int{"m": req}
		}
		state = recoveredGuardView(state, req)
	}
	calibrated := state
	calibrated.calibrated = true
	return []guardState{state, calibrated}
}

// recoveredGuardView invokes real recovery against immutable synthetic entries.
// It updates the fake provider's rendered byte charge from that projection.
func recoveredGuardView(state guardState, req int) guardState {
	facts := contextmeasure.RequestFacts{Model: "m", TextBytes: req * 3, ViewTextBytes: state.view, ViewBearing: true}
	if state.known {
		facts.ContextWindow = window * unit
	}
	models := map[string]compaction.ModelState{"m": {Rate: contextmeasure.Rate{Tokens: 1, Bytes: 3}, Calibrated: state.calibrated}}
	recovery := compaction.Recover(compaction.State{Models: models}, guardEntries(state), state.projection, contextmeasure.ContextOverflow{Failed: facts}, compaction.Settings{})
	state.projection = recovery.View
	state.view = guardRenderedBytes(state)
	if !recovery.Reached {
		state.step.GuardOff = true
	}
	return state
}

func guardEntries(state guardState) []index.MessageEntry {
	return []index.MessageEntry{{ID: 1, Kind: index.Assistant, Role: "assistant", ToolUseIDs: []string{"call"}}, {ID: 2, Kind: index.ToolResult, Role: "user", ToolUseIDs: []string{"call"}, Bytes: state.initial, Keep: true}}
}

func guardRenderedBytes(state guardState) int {
	if state.projection.ForceClearThrough >= 2 {
		return len(checkpoint.ClearedResult(state.initial, "call", "")) + len("call") + 2
	}
	if state.projection.PreviewThrough >= 2 {
		return min(state.initial, state.projection.Budget.PreviewTokens*checkpoint.BytesPerToken*6+len("call")+2)
	}
	return state.initial
}

func guardInvariants(state guardState) map[string]bool {
	explained := state.status != failed
	shrinking := true
	for i, sent := range state.sent {
		if !sent.overflow {
			continue
		}
		if state.failure == noProgress && state.failRequest >= sent.size {
			explained = true
		}
		for _, later := range state.sent[i+1:] {
			shrinking = shrinking && later.size < sent.size
		}
	}
	if state.status == failed && state.failure != noProgress && state.failure != "guard" {
		explained = len(state.sent) > 0 && state.sent[len(state.sent)-1].overflow
	}
	fits := state.status != success || (len(state.sent) > 0 && !state.sent[len(state.sent)-1].overflow && state.sent[len(state.sent)-1].size <= window*unit)
	return map[string]bool{
		"FailsOnlyOnProvider": explained,
		"RetriesShrink":       shrinking,
		"BudgetFailureHonest": state.failure != budgetFailure || overflowCount(state.sent) == 3,
		"SuccessFits":         fits,
	}
}

func overflowCount(sent []sentRequest) int {
	count := 0
	for _, req := range sent {
		if req.overflow {
			count++
		}
	}
	return count
}
