package judge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Claim names one criterion to judge against the subject.
type Claim struct{ ID, Text string }

// Input carries an already rendered subject and explicit task and shared facts.
// Subject and claim IDs match [a-z0-9_]{1,64}.
type Input struct {
	Subject   string
	Text      string
	Truncated bool
	Task      string
	Reference string
	Claims    []Claim
}

// Judgment is one claim's label and required rationale.
type Judgment struct {
	ClaimID   string
	Label     Label
	Rationale string
}

// Result preserves input claim order. Usage and Latency cover judgment calls,
// including corrections, but exclude calibration.
type Result struct {
	Judgments                     []Judgment
	Calibrated, Truncated, NoCall bool
	Corrections                   int
	Usage                         model.TokenUsage
	Latency                       time.Duration
}

// Config configures deterministic forced-tool judging.
type Config struct {
	// Client must support concurrent calls when Judge is used concurrently.
	Client model.Client
	// ModelID and a positive MaxOutputTokens are required.
	ModelID         string
	MaxOutputTokens int
	// MaxInputBytes defaults to 512 KiB and includes the entire encoded request.
	MaxInputBytes int
	Instructions  string
	// Live requires explicit LOOM_LIVE_MODEL=1 opt-in when true.
	Live     bool
	Examples []Example
	// Cache defaults to the process-wide memory cache.
	Cache Cache
	// CalibrationTimeout defaults to two minutes.
	CalibrationTimeout time.Duration
	// Recalibrate bypasses cached calibrations for this Judge.
	Recalibrate bool
}

// Judge evaluates claims using a fixed model and prompt contract.
type Judge struct {
	cfg            Config
	promptHash     string
	examples       []Example
	calibrationKey string
	calibrationMu  sync.Mutex
	calibration    *calibrationAttempt
}

var (
	// ErrInputTooLarge means the complete encoded request exceeds MaxInputBytes.
	ErrInputTooLarge = errors.New("judge: input too large")
	// ErrSchemaTooLarge means the claim schema exceeds 1 MiB.
	ErrSchemaTooLarge = errors.New("judge: schema too large")
	// ErrInvalidOutput means the bounded correction budget was exhausted.
	ErrInvalidOutput = errors.New("judge: invalid output")
	// ErrInvalidInput means an input or configuration violates the judge contract.
	ErrInvalidInput = errors.New("judge: invalid input")
)

// New validates cfg and fixes the model and prompt identity.
func New(cfg Config) (*Judge, error) {
	if cfg.Live {
		enabled, err := agenttest.LiveModelEnabled()
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, fmt.Errorf("judge: live model %q: %w", cfg.ModelID, agenttest.ErrLiveModelDisabled)
		}
	}
	if cfg.CalibrationTimeout < 0 {
		return nil, fmt.Errorf("%w: negative calibration timeout", ErrInvalidInput)
	}
	if cfg.CalibrationTimeout == 0 {
		cfg.CalibrationTimeout = 2 * time.Minute
	}
	if cfg.Cache == nil {
		cfg.Cache = processCache
	}
	examples, err := prepareExamples(cfg.Examples)
	if err != nil {
		return nil, err
	}
	cfg.Examples = nil

	if cfg.Client == nil || strings.TrimSpace(cfg.ModelID) == "" || !utf8.ValidString(cfg.ModelID) || cfg.MaxOutputTokens <= 0 || cfg.MaxInputBytes < 0 || !utf8.ValidString(cfg.Instructions) {
		return nil, fmt.Errorf("%w: client, model ID, positive output cap and valid input cap/instructions are required", ErrInvalidInput)
	}
	if cfg.MaxInputBytes == 0 {
		cfg.MaxInputBytes = 512 * 1024
	}
	hash, err := digest(map[string]any{"schema": "loom-mcp.eval.judge/v1", "system": systemText, "tool_description": toolDescription, "user_template": map[string]string{"format": userTemplate, "truncation_notice": truncationNotice}, "instructions": cfg.Instructions})
	if err != nil {
		return nil, err
	}
	key, err := digest(map[string]any{"prompt_hash": hash, "model_id": cfg.ModelID, "max_output_tokens": cfg.MaxOutputTokens, "examples": examples})
	if err != nil {
		return nil, err
	}
	return &Judge{cfg: cfg, promptHash: hash, examples: examples, calibrationKey: key}, nil
}

// ModelID returns the configured model identifier.
func (j *Judge) ModelID() string {
	return j.cfg.ModelID
}

// PromptHash returns the canonical prompt contract's sha256:<hex> digest.
func (j *Judge) PromptHash() string {
	return j.promptHash
}

// Judge evaluates every claim, with at most three corrections.
func (j *Judge) Judge(ctx context.Context, in Input) (*Result, error) {
	req, err := j.request(in)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Text) == "" {
		result := &Result{NoCall: true, Truncated: in.Truncated, Judgments: make([]Judgment, len(in.Claims))}
		for i, claim := range in.Claims {
			result.Judgments[i] = Judgment{ClaimID: claim.ID, Label: NotAddressed, Rationale: "The subject is empty."}
		}
		return result, nil
	}
	calibration, calibrationErr := j.Calibrate(ctx)
	result, err := j.call(ctx, req, in, 3)
	result.Calibrated = calibrationErr == nil && calibration != nil && calibration.Passed
	return result, err
}
