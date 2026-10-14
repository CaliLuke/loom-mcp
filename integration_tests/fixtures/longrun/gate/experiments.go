package suite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"example.com/longrun"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// Report is a reproducible stored-only evaluation plus informational costs.
type Report struct {
	// Variants presents the latest decisions, including ineligible siblings.
	// Decisions remains the eligible-only prefix used by operational scheduling.
	Variants    []gate.VariantDecision `json:"variants"`
	Evaluations []gate.Evaluation      `json:"evaluations"`
	Decisions   []gate.Decision        `json:"decisions"`
	Diagnostics map[string]Diagnostics `json:"diagnostics"`
}

// RunExperiment persists every attempt before returning the final immutable export.
func RunExperiment(ctx context.Context, store *eval.Store, cfg eval.Config, meta eval.ExperimentMeta) (*eval.Experiment, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer, err := store.Begin(ctx, cfg, meta)
	if err != nil {
		return nil, err
	}
	cfg.OnAttempt = writer.OnAttempt(func(*eval.AttemptRecord) {
		if writer.Err() != nil {
			cancel()
		}
	})
	report, runErr := eval.Run(ctx, cfg)
	if report == nil {
		return nil, runErr
	}
	finishErr := writer.Finish(report, runErr)
	exp, loadErr := store.Load(writer.ID())
	return exp, errors.Join(runErr, finishErr, loadErr)
}

// StoredReport verifies frozen identities and re-evaluates each cumulative look.
// It never calls Harness or a provider constructor.
func StoredReport(dir, config string, store *eval.Store, verify bool) (Report, error) {
	f, err := VerifyFreeze(dir, config)
	if err != nil {
		return Report{}, err
	}
	ids, err := readLooks(dir, config)
	if err != nil {
		return Report{}, err
	}
	return reportLooks(dir, config, store, f, ids, verify)
}
func reportLooks(dir, config string, store *eval.Store, f gate.Freeze, ids []string, verify bool) (Report, error) {
	out := Report{Diagnostics: map[string]Diagnostics{}}
	var attempts []eval.StoredAttempt
	for i, id := range ids {
		look := i + 1
		work, err := PlanLook(f.Policy, look, out.Decisions)
		if err != nil {
			return out, err
		}
		planInformational(out.Diagnostics, f, work)
		current, err := Overlay(store, id, f, look)
		if err != nil {
			return out, err
		}
		if verify {
			if err := verifyAttempts(dir, current); err != nil {
				return out, err
			}
		}
		attempts = append(attempts, current...)
		manifest, err := Manifest(dir, config, look)
		if err != nil {
			return out, err
		}
		cfg := GateConfig(f.GatedScenarios, f.Policy)
		cfg.Variants = slices.DeleteFunc(slices.Clone(work.Variants), func(id string) bool {
			return id == "baseline" || id == "clear_only"
		})
		cfg.Items = manifest
		evaluation, decision, err := Analyze(attempts, cfg, look, out.Decisions)
		if err != nil {
			return out, err
		}
		out.Evaluations = append(out.Evaluations, evaluation)
		out.Decisions = append(out.Decisions, decision)
	}
	diagnostics, err := Describe(attempts)
	maps.Copy(out.Diagnostics, diagnostics)
	if len(out.Decisions) > 0 {
		out.Variants = slices.Clone(out.Decisions[len(out.Decisions)-1].Variants)
	}
	for _, selection := range f.Selections {
		if selection.Status == gate.CalibrationIneligible {
			out.Variants = append(out.Variants, gate.VariantDecision{Variant: selection.Variant, Status: selection.Status})
		}
	}
	return out, err
}

// Look runs only the new partition, or missing groups through an explicit Parent rerun.
func Look(ctx context.Context, dir, config, binding string, store *eval.Store, look, workers int) (Report, error) {
	f, err := VerifyFreeze(dir, config)
	if err != nil {
		return Report{}, err
	}
	ids, err := readLooks(dir, config)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Report{}, err
	}
	if look < 1 || look > 3 || look > len(ids)+1 || look < len(ids) {
		return Report{}, fmt.Errorf("out-of-order look %d", look)
	}
	var prior Report
	if len(ids) > 0 {
		prior, err = reportLooks(dir, config, store, f, ids, true)
		if err != nil {
			return prior, err
		}
	}
	work, err := PlanLook(f.Policy, look, prior.Decisions)
	if err != nil {
		return prior, err
	}
	parent := ""
	selection := eval.Selection{Keys: work.Keys}
	if work.Rerun {
		parent = ids[look-1]
	}
	ss, err := Scenarios(dir, config, fmt.Sprintf("look%d", look))
	if err != nil {
		return prior, err
	}
	variants := scenarios.Variants()
	variants = slices.DeleteFunc(variants, func(v eval.Variant) bool {
		return !slices.Contains(work.Variants, v.ID)
	})
	label, err := f.Label(look)
	if err != nil {
		return prior, err
	}
	cfg := eval.Config{Harness: longrun.Harness(binding, fake.Faults{}), Scenarios: ss, Variants: variants, Repeats: 2, Concurrency: workers, Select: selection}
	exp, err := RunExperiment(ctx, store, cfg, eval.ExperimentMeta{Label: label, Parent: parent})
	runErr := err
	if exp == nil {
		return prior, runErr
	}
	if look <= len(ids) {
		ids[look-1] = exp.Header.ID
	} else {
		ids = append(ids, exp.Header.ID)
	}
	if err := writeLooks(dir, config, ids); err != nil {
		return prior, err
	}
	out, reportErr := reportLooks(dir, config, store, f, ids, true)
	return out, errors.Join(runErr, reportErr)
}

// GateConfig binds explicit policy; callers add the exact planned Items.
func GateConfig(ids []string, policy *gate.Policy) gate.Config {
	variants := []string{}
	if policy != nil {
		for _, id := range []string{"receipt_only", "hybrid"} {
			if _, ok := policy.Unsupported[id]; ok {
				variants = append(variants, id)
			}
		}
	}
	return gate.Config{Policy: policy, Baseline: "baseline", Variants: variants, Scenarios: slices.Clone(ids), Repeats: 2}
}

// ProposedOfflinePolicy supplies explicit unselected margins for offline proofs.
// It never authorizes a qualifying look; those require a verified v2 freeze.
func ProposedOfflinePolicy() *gate.Policy {
	p := gate.ProposedPolicy(map[string]float64{"receipt_only": .02, "hybrid": .02}, BootstrapSeed)
	return &p
}
func verifyAttempts(dir string, attempts []eval.StoredAttempt) error {
	items := map[string]eval.Item{}
	for _, a := range attempts {
		if _, ok := items[a.Item]; !ok {
			for _, part := range []string{"pilot", "look1", "look2", "look3", "smoke"} {
				d, err := LoadPool(dir, a.Scenario, part)
				if err != nil {
					if a.Scenario == "model_change" && part == "pilot" {
						continue
					}
					return err
				}
				for _, item := range d.Items() {
					items[item.ID] = item
				}
			}
		}
		item, ok := items[a.Item]
		if !ok {
			return fmt.Errorf("stored item outside frozen pools: %s", a.Item)
		}
		if err := VerifyMeasures(a, item); err != nil {
			return err
		}
	}
	return nil
}
func readLooks(dir, config string) ([]string, error) {
	var ids []string
	b, err := readFile(dir, filepath.Join("gate", "looks_"+config+".json"))
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(b, &ids)
	return ids, err
}
func writeLooks(dir, config string, ids []string) error {
	b, err := json.MarshalIndent(ids, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "gate", "looks_"+config+".json")
	if err := os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
