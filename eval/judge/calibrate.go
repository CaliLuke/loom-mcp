package judge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Example supplies a labeled calibration input. User examples follow the built-ins.
type Example struct {
	ID    string
	Input Input
	Want  map[string]Label
}

// Calibration records a completed label comparison or a partial infrastructure failure.
// Completed is zero for infrastructure failures, which are never cached.
type Calibration struct {
	Key, ModelID, PromptHash string
	Passed                   bool
	Examples                 []ExampleResult
	Completed                time.Time
}

// ExampleResult records expected and observed labels and any output diagnostic.
type ExampleResult struct {
	ID        string
	Want, Got map[string]Label
	Err       string
}

// calibrationAttempt publishes an immutable outcome to its own waiters on close.
type calibrationAttempt struct {
	done   chan struct{}
	result *Calibration
	err    error
}

// CalibrationKey returns the canonical model, prompt, output-cap and examples digest.
func (j *Judge) CalibrationKey() string {
	return j.calibrationKey
}

// Calibrate shares one attempt's outcome with concurrent callers.
// The first caller's context controls the run; other waiters can cancel their wait.
// Caller cancellation permits a later retry. Other infrastructure errors are
// retained only by this Judge; completed passes and failures are cached.
func (j *Judge) Calibrate(ctx context.Context) (*Calibration, error) {
	j.calibrationMu.Lock()
	first := j.calibration == nil
	if first {
		j.calibration = &calibrationAttempt{done: make(chan struct{})}
	}
	attempt := j.calibration
	j.calibrationMu.Unlock()
	if first {
		attempt.result, attempt.err = j.runCalibration(ctx)
		j.calibrationMu.Lock()
		if ctx.Err() != nil && errors.Is(attempt.err, ctx.Err()) {
			j.calibration = nil
		}
		close(attempt.done)
		j.calibrationMu.Unlock()
	}
	select {
	case <-attempt.done:
		return cloneCalibration(attempt.result), attempt.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func builtInExamples() []Example {
	labels := []Label{Entailed, Contradicted, NotAddressed, Indeterminate}
	texts := []string{"The package arrives on Monday.", "The package arrives on Tuesday, not Monday.", "The package is in a red box.", "The package might arrive on Monday or Tuesday; the day is not yet known."}
	examples := make([]Example, len(labels))
	for i, label := range labels {
		examples[i] = Example{ID: string(label), Input: Input{Subject: "answer", Text: texts[i], Task: "State when the package arrives.", Claims: []Claim{{ID: "arrival", Text: "The package arrives on Monday."}}}, Want: map[string]Label{"arrival": label}}
	}
	return examples
}

// prepareExamples owns the supplied data and verifies complete expected label sets.
func prepareExamples(extra []Example) ([]Example, error) {
	examples := append(builtInExamples(), extra...)
	seen := make(map[string]bool, len(examples))
	for i, example := range examples {
		if example.ID == "" || !utf8.ValidString(example.ID) || seen[example.ID] {
			return nil, fmt.Errorf("%w: calibration example IDs must be non-empty, unique UTF-8", ErrInvalidInput)
		}
		seen[example.ID] = true
		if err := validateInput(example.Input); err != nil {
			return nil, fmt.Errorf("example %q: %w", example.ID, err)
		}
		if len(example.Want) != len(example.Input.Claims) {
			return nil, fmt.Errorf("%w: example %q requires every expected label", ErrInvalidInput, example.ID)
		}
		for _, claim := range example.Input.Claims {
			if !example.Want[claim.ID].Valid() {
				return nil, fmt.Errorf("%w: example %q has a missing or invalid label", ErrInvalidInput, example.ID)
			}
		}
		examples[i].Input.Claims = slices.Clone(example.Input.Claims)
		examples[i].Want = maps.Clone(example.Want)
	}
	return examples, nil
}

func (j *Judge) runCalibration(ctx context.Context) (*Calibration, error) {
	if !j.cfg.Recalibrate {
		if cached, ok := j.cfg.Cache.Load(j.calibrationKey); ok {
			return cached, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, j.cfg.CalibrationTimeout)
	defer cancel()
	calibration := &Calibration{Key: j.calibrationKey, ModelID: j.ModelID(), PromptHash: j.PromptHash(), Passed: true}
	for _, example := range j.examples {
		result, err := j.calibrateExample(ctx, example)
		calibration.Examples = append(calibration.Examples, result)
		if err != nil {
			calibration.Passed = false
			return calibration, err
		}
		if result.Err != "" || !maps.Equal(result.Want, result.Got) {
			calibration.Passed = false
		}
	}
	calibration.Completed = time.Now().UTC()
	if err := j.cfg.Cache.Store(calibration); err != nil {
		calibration.Passed = false
		calibration.Completed = time.Time{}
		return calibration, fmt.Errorf("judge: store calibration: %w", err)
	}
	return calibration, nil
}

func (j *Judge) calibrateExample(ctx context.Context, example Example) (ExampleResult, error) {
	result := ExampleResult{ID: example.ID, Want: maps.Clone(example.Want), Got: make(map[string]Label)}
	req, err := j.request(example.Input)
	if err != nil {
		result.Err = err.Error()
		return result, err
	}
	if strings.TrimSpace(example.Input.Text) == "" {
		for _, claim := range example.Input.Claims {
			result.Got[claim.ID] = NotAddressed
		}
		return result, nil
	}
	judged, err := j.call(ctx, req, example.Input, 0)
	if err != nil {
		result.Err = err.Error()
		if errors.Is(err, ErrInvalidOutput) {
			return result, nil
		}
		return result, err
	}
	for _, judgment := range judged.Judgments {
		result.Got[judgment.ClaimID] = judgment.Label
	}
	return result, nil
}
