package evalcli

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
)

type runDocument struct {
	Format      string         `json:"format"`
	Version     int            `json:"version"`
	Experiment  jsontext.Value `json:"experiment"`
	Comparisons jsontext.Value `json:"comparisons"`
}

const runFormat = "loom-mcp.eval.run"

func run(ctx context.Context, cfg Config, f flags) error {
	if _, err := agenttest.LiveModelEnabled(); err != nil {
		return usage(err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	dir, err := storeDir(cfg, f)
	if err != nil {
		return err
	}
	store, err := eval.Open(dir, eval.WithOnError(cancel))
	if err != nil {
		return err
	}
	runner := eval.Config{Harness: cfg.Harness, AttemptHarness: cfg.AttemptHarness, Scenarios: cfg.Scenarios, Variants: cfg.Variants, Select: f.selection, Repeats: f.repeat, Concurrency: f.concurrency, MaxTokens: f.maxTokens}
	parent, err := rerunConfig(store, f, &runner)
	if errors.Is(err, eval.ErrNothingToRerun) {
		return nothingToRerun(cfg, f)
	}
	if err != nil {
		return err
	}
	keys, err := eval.Plan(runner)
	if err != nil {
		return usage(err)
	}
	runner.Judge, err = newJudge(cfg, f, dir)
	if err != nil {
		return err
	}
	identityDir := cfg.Dir
	if identityDir == "" {
		identityDir = "."
	}
	identity := eval.DetectIdentity(ctx, identityDir)
	writer, err := store.Begin(ctx, runner, eval.ExperimentMeta{Label: f.label, Parent: parent, Identity: &identity})
	if err != nil {
		return err
	}
	if cfg.CaptureEvidence {
		captureEvidence(writer, &runner)
	}
	runner.OnAttempt = writer.OnAttempt(progress(cfg, f, writer, len(keys), cancel))
	return executeRun(ctx, cfg, f, store, writer, runner)
}

// captureEvidence binds either supported factory before the runner constructs a harness.
func captureEvidence(writer *eval.Writer, runner *eval.Config) {
	factory := runner.AttemptHarness
	if factory == nil {
		legacy := runner.Harness
		factory = func(ctx context.Context, info eval.AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
			return legacy(ctx, info.Variant, opts...)
		}
	}
	runner.Harness = nil
	runner.AttemptHarness = writer.CaptureHarness(factory)
}

func executeRun(ctx context.Context, cfg Config, f flags, store *eval.Store, writer *eval.Writer, runner eval.Config) error {
	report, runErr := eval.Run(ctx, runner)
	if report.Judge != nil {
		writer.SetJudge(*report.Judge)
	}
	finishErr := writer.Finish(report, runErr)
	if storeErr := errors.Join(writer.Err(), finishErr); storeErr != nil {
		return errors.Join(storeErr, runErr, fallbackSummary(cfg, f, report))
	}
	e, err := store.Load(writer.ID())
	if err != nil {
		return errors.Join(err, runErr)
	}
	outputErr := writeRunOutput(cfg, f, store, e)
	if errors.Is(runErr, agenttest.ErrLiveModelDisabled) {
		_, err := fmt.Fprintln(cfg.Stderr, "live model disabled: set LOOM_LIVE_MODEL=1 to run live variants")
		runErr = errors.Join(runErr, err)
	}
	return errors.Join(runErr, outputErr)
}

func rerunConfig(store *eval.Store, f flags, runner *eval.Config) (string, error) {
	if f.rerun == "" {
		return "", nil
	}
	parent, err := loadRef(store, f.rerun)
	if err != nil {
		return "", err
	}
	runner.Select, err = parent.RerunSelection()
	if err != nil {
		return "", err
	}
	if !f.repeatSet {
		runner.Repeats = parent.Header.Config.Repeats
	}
	return parent.Header.ID, nil
}

func nothingToRerun(cfg Config, f flags) error {
	if f.json {
		_, err := fmt.Fprintln(cfg.Stderr, "nothing to rerun")
		return errors.Join(err, writeJSON(cfg.Stdout, runDocument{Format: runFormat, Version: 1}))
	}
	_, err := fmt.Fprintln(cfg.Stdout, "nothing to rerun")
	return err
}

func writeRunOutput(cfg Config, f flags, store *eval.Store, e *eval.Experiment) error {
	if !f.json {
		if err := eval.Summary(cfg.Stdout, e); err != nil {
			return err
		}
	}
	comparisons, compareErr := runComparison(cfg, f, store, e)
	if f.json {
		return errors.Join(compareErr, runSummaryJSON(cfg, e, comparisons))
	}
	return errors.Join(compareErr, eval.WriteComparisons(cfg.Stdout, comparisons))
}

func runSummaryJSON(cfg Config, e *eval.Experiment, comparisons []*eval.Comparison) error {
	var summary, compared bytes.Buffer
	if err := eval.SummaryJSON(&summary, e); err != nil {
		return err
	}
	document := runDocument{Format: runFormat, Version: 1, Experiment: jsontext.Value(summary.Bytes())}
	if comparisons != nil {
		if err := eval.WriteComparisonsJSON(&compared, comparisons); err != nil {
			return err
		}
		document.Comparisons = jsontext.Value(compared.Bytes())
	}
	return writeJSON(cfg.Stdout, document)
}

// fallbackSummary labels volatile counts; it never presents them as stored evidence.
func fallbackSummary(cfg Config, f flags, report *eval.Report) error {
	if f.json {
		return writeJSON(cfg.Stdout, struct {
			runDocument
			Groups []eval.Group `json:"unsaved_groups"`
		}{runDocument: runDocument{Format: runFormat, Version: 1}, Groups: report.Groups()})
	}
	var out bytes.Buffer
	fmt.Fprintln(&out, "Store failed; in-memory verdict counts:")
	for _, g := range report.Groups() {
		fmt.Fprintf(&out, "%s/%s [%s] %d passed, %d failed, %d error, %d pending, %d not run\n", g.Scenario, g.Item, g.Variant, g.Passed, g.Failed, g.Errored, g.Pending, g.NotRun)
	}
	_, err := cfg.Stdout.Write(out.Bytes())
	return err
}
