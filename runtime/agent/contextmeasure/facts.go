// Package contextmeasure measures model requests without recording or rejecting them.
package contextmeasure

import (
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// RequestOutcome classifies a completed request independently of provider details.
type RequestOutcome string

// RequestFacts carries the measurements of one request and its eventual outcome.
type RequestFacts struct {
	Call          runstore.CallKey `json:"call"`
	Model         string           `json:"model"`
	ContextWindow int              `json:"context_window"`
	OutputReserve int              `json:"output_reserve"`
	ViewBearing   bool             `json:"view_bearing"`
	InputTokens   int              `json:"input_tokens"`
	TextBytes     int              `json:"text_bytes"`
	FixedBytes    int              `json:"fixed_bytes"`
	NonText       int              `json:"non_text"`
	CountLatency  time.Duration    `json:"count_latency"`
	Outcome       RequestOutcome   `json:"outcome"`
}

const (
	// OutcomeOK records a successful provider request.
	OutcomeOK RequestOutcome = "ok"
	// OutcomeOverflow records a provider context overflow.
	OutcomeOverflow RequestOutcome = "overflow"
	// OutcomePreSendRejected is reserved for K14 guard rejection wiring.
	OutcomePreSendRejected RequestOutcome = "pre_send_rejected"
	// OutcomeError records all other request failures.
	OutcomeError RequestOutcome = "error"
)

// WithResult returns completed facts, preserving the pre-send measurements.
func (f RequestFacts) WithResult(usage model.TokenUsage, err error) RequestFacts {
	f.InputTokens = addSize(addSize(usage.InputTokens, usage.CacheReadTokens), usage.CacheWriteTokens)
	f.Outcome = OutcomeOK
	if err != nil {
		f.Outcome = OutcomeError
		if providerErr, ok := model.AsProviderError(err); ok && providerErr != nil && providerErr.Kind() == model.ProviderErrorKindContextOverflow {
			f.Outcome = OutcomeOverflow
		}
	}
	return f
}
