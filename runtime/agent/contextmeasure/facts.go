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
	// LimitsInvalid distinguishes an invalid merged override from absent limits.
	LimitsInvalid bool             `json:"limits_invalid,omitzero"`
	Call          runstore.CallKey `json:"call"`
	Model         string           `json:"model"`
	ContextWindow int              `json:"context_window"`
	OutputReserve int              `json:"output_reserve"`
	ViewBearing   bool             `json:"view_bearing"`
	ViewTextBytes int              `json:"view_text_bytes,omitzero"`
	ViewNonText   int              `json:"view_non_text,omitzero"`
	InputTokens   int              `json:"input_tokens"`
	TextBytes     int              `json:"text_bytes"`
	FixedBytes    int              `json:"fixed_bytes"`
	NonText       int              `json:"non_text"`
	CountLatency  time.Duration    `json:"count_latency"`
	Outcome       RequestOutcome   `json:"outcome"`
	// RecoveryToolBytes measures definitions added only after shortening the view.
	// It is excluded from the failed request's TextBytes and FixedBytes.
	RecoveryToolBytes int `json:"recovery_tool_bytes,omitzero"`
}

// ContextOverflow identifies the durable call that stopped a planner attempt.
// Source is provider, guard or no_progress; Ordinal is diagnostic only.
type ContextOverflow struct {
	Provider      *ProviderFailure `json:"provider,omitzero"`
	Source        string           `json:"source"`
	Failed        RequestFacts     `json:"failed"`
	Ordinal       int              `json:"ordinal"`
	ViewTextBytes int              `json:"view_text_bytes"`
}

// ProviderFailure carries provider overflow identity across the activity boundary.
// Native SDK causes stay in the call error text; this evidence reconstructs the
// stable ProviderError contract during deterministic workflow recovery.
type ProviderFailure struct {
	Provider   string `json:"provider"`
	Operation  string `json:"operation"`
	HTTPStatus int    `json:"http_status"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id"`
}

const (
	// OverflowProvider marks an observed provider overflow.
	OverflowProvider = "provider"
	// OverflowGuard marks an advisory pre-send guard rejection.
	OverflowGuard = "guard"
	// OverflowNoProgress marks a request at least as large as a rejected request.
	OverflowNoProgress = "no_progress"

	// OutcomeOK records a successful provider request.
	OutcomeOK RequestOutcome = "ok"
	// OutcomeOverflow records a provider context overflow.
	OutcomeOverflow RequestOutcome = "overflow"
	// OutcomePreSendRejected records an advisory guard rejection.
	OutcomePreSendRejected RequestOutcome = "pre_send_rejected"
	// OutcomeError records all other request failures.
	OutcomeError RequestOutcome = "error"
)

// CaptureProviderFailure snapshots a provider error for replay.
func CaptureProviderFailure(e *model.ProviderError) *ProviderFailure {
	return &ProviderFailure{Provider: e.Provider(), Operation: e.Operation(), HTTPStatus: e.HTTPStatus(), Code: e.Code(), Message: e.Message(), RequestID: e.RequestID()}
}

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

// Restore restores the public provider overflow category without live SDK state.
func (f *ProviderFailure) Restore() *model.ProviderError {
	return model.NewProviderError(f.Provider, f.Operation, f.HTTPStatus, model.ProviderErrorKindContextOverflow, f.Code, f.Message, f.RequestID, false, nil)
}
