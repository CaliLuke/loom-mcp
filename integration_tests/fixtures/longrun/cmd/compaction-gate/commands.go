package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"example.com/longrun"
	"example.com/longrun/fake"
	suite "example.com/longrun/gate"
	"example.com/longrun/internal/calibrationprogress"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

type options struct {
	dir, config, out, pilot, inventory, base string
	offline, verify, declareComplete         bool
	seed                                     uint64
	workers                                  int
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	// This probe is deliberately before flag parsing and all fixture/provider paths.
	if len(args) == 1 && args[0] == "_source-executable" {
		id, err := suite.SourceExecutableID(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		if _, err := fmt.Fprintln(stdout, id); err != nil {
			return fail(stderr, err)
		}
		return 0
	}
	if len(args) == 0 {
		return fail(stderr, fmt.Errorf("commands: items validate pilot calibrate freeze look N report run show compare list"))
	}
	command := args[0]
	args = args[1:]
	o := options{}
	flags := flag.NewFlagSet("compaction-gate "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&o.dir, "dir", defaultDir(), "fixture module directory")
	flags.StringVar(&o.config, "config", "offline", "provider configuration")
	flags.StringVar(&o.out, "out", "", "experiment store")
	flags.StringVar(&o.pilot, "pilot", "", "baseline pilot experiment ID")
	flags.StringVar(&o.inventory, "inventory", "", "complete historical store inventory")
	flags.StringVar(&o.base, "base", "origin/main", "retained source base revision")
	flags.BoolVar(&o.declareComplete, "declare-complete-store", false, "declare --out as the complete historical store")
	flags.BoolVar(&o.offline, "offline", false, "use Reader under the selected configuration")
	flags.BoolVar(&o.verify, "verify", false, "recompute stored measures from exports")
	flags.Uint64Var(&o.seed, "seed", 19, "pool generation seed")
	flags.IntVar(&o.workers, "workers", 4, "parallel attempts or calibration cells")
	look := 0
	if command == "look" && len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return fail(stderr, err)
		}
		look = n
		args = args[1:]
	}
	if command == "run" || command == "show" || command == "compare" || command == "list" {
		return embedded(ctx, command, args, stdout, stderr)
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || o.workers < 1 {
		return fail(stderr, fmt.Errorf("unexpected arguments or invalid worker count"))
	}
	if err := evidencePreflight(command, o); err != nil {
		return fail(stderr, err)
	}
	var result any
	var err error
	switch command {
	case "inventory", "archive-v1", "source":
		result, err = evidence(ctx, command, o)
	case "items":
		err = suite.WriteItems(o.dir, o.seed)
	case "validate":
		result, err = validate(ctx, o, stderr)
	case "calibrate":
		err = calibrate(ctx, o, stderr)
	case "freeze":
		result, err = freeze(ctx, o)
	case "pilot", "look", "report":
		result, err = experiment(ctx, command, look, o)
	default:
		return fail(stderr, fmt.Errorf("unknown command %q", command))
	}
	if err != nil {
		return fail(stderr, err)
	}
	if result != nil {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return fail(stderr, err)
		}
	}
	return 0
}
func binding(o options) (string, error) {
	if o.offline && o.config != "offline" {
		return "", fmt.Errorf("--offline requires --config offline for pilot and look")
	}
	if o.config == "offline" {
		return "offline", nil
	}
	enabled, err := agenttest.LiveModelEnabled()
	if err != nil {
		return "", err
	}
	if !enabled {
		return "", agenttest.ErrLiveModelDisabled
	}
	return o.config, nil
}
func storeFor(o options) (*eval.Store, error) {
	if o.out == "" {
		o.out = filepath.Join(o.dir, ".loom-evals")
	}
	return eval.Open(o.out)
}
func validate(ctx context.Context, o options, progress io.Writer) (suite.Validation, error) {
	before, err := suite.ReadSource(ctx, o.dir, o.config)
	if err != nil {
		return suite.Validation{}, err
	}
	if err := suite.VerifyEvidenceExecutable(ctx, o.dir); err != nil {
		return suite.Validation{}, err
	}
	v, runErr := suite.ValidateConfiguration(ctx, o.dir, o.config, before.Identity.SHA256, o.workers, progress)
	after, err := suite.ReadSource(ctx, o.dir, o.config)
	if err != nil {
		return v, errors.Join(runErr, err)
	}
	if before.SHA256 != after.SHA256 {
		return v, errors.Join(runErr, fmt.Errorf("source changed during validation"))
	}
	return v, errors.Join(runErr, suite.WriteValidation(o.dir, v))
}
func calibrate(ctx context.Context, o options, output io.Writer) (runErr error) {
	progress := calibrationprogress.Start(ctx, output)
	defer func() { runErr = progress.Close(runErr) }()
	ctx = gate.WithCalibrationProgress(progress.Context(), progress.Observe)
	if o.config != "offline" && o.pilot == "" {
		return fmt.Errorf("live calibration requires a complete --pilot")
	}
	v, err := suite.ReadValidationFor(o.dir, o.config)
	if err != nil {
		return err
	}
	cfg := suite.CalibrationConfig(o.config, v, o.workers)
	var pilot *suite.PilotEvidence
	if o.pilot != "" {
		source, err := suite.ReadSource(ctx, o.dir, o.config)
		if err != nil {
			return err
		}
		if source.Identity.SHA256 != v.SourceSHA256 {
			return fmt.Errorf("validation source mismatch")
		}
		if err := suite.VerifyEvidenceExecutable(ctx, o.dir); err != nil {
			return err
		}
		storePath := o.out
		if storePath == "" {
			storePath = filepath.Join(o.dir, ".loom-evals")
		}
		if _, err := os.Stat(storePath); err != nil {
			return err
		}
		store, err := storeFor(o)
		if err != nil {
			return err
		}
		id, err := store.Resolve(o.pilot)
		if err != nil {
			return err
		}
		exp, err := store.Load(id)
		if err != nil {
			return err
		}
		evidence, err := suite.ReadPilotEvidence(o.dir, o.config, exp, v.Profile, source.Identity.SHA256)
		if err != nil {
			return err
		}
		pilot = &evidence
		cfg.Pilot, err = suite.VerifyPilot(o.dir, o.config, evidence, v.Profile, source.Identity.SHA256)
		if err != nil {
			return err
		}
	}
	return suite.Calibrate(ctx, o.dir, o.config, cfg, pilot, progress.Phase)
}
func freeze(ctx context.Context, o options) (gate.Freeze, error) {
	f, err := suite.BuildVerifiedFreeze(ctx, o.dir, o.config, o.inventory)
	if err != nil {
		return f, err
	}
	return f, suite.WriteFreeze(o.dir, f)
}
func experiment(ctx context.Context, command string, look int, o options) (any, error) {
	if command == "look" && (look < 1 || look > 3) {
		return nil, fmt.Errorf("out-of-order look %d", look)
	}
	if command == "report" {
		if _, err := suite.VerifyFreeze(o.dir, o.config); err != nil {
			return nil, err
		}
		if err := suite.VerifyEvidenceExecutable(ctx, o.dir); err != nil {
			return nil, err
		}
		store, err := storeFor(o)
		if err != nil {
			return nil, err
		}
		return suite.StoredReport(o.dir, o.config, store, o.verify)
	}
	bind, err := binding(o)
	if err != nil {
		return nil, err
	}
	profile, err := longrun.ResolveProfile(o.config)
	if err != nil {
		return nil, err
	}
	source, err := suite.ReadSource(ctx, o.dir, o.config)
	if err != nil {
		return nil, err
	}
	validation, err := suite.ReadValidationFor(o.dir, o.config)
	if err != nil {
		return nil, err
	}
	if source.Identity.SHA256 != validation.SourceSHA256 {
		return nil, fmt.Errorf("validation source mismatch")
	}
	if err := suite.VerifyLiveProfile(o.config, validation.Profile.Roles); err != nil {
		return nil, err
	}
	if err := suite.VerifyEvidenceExecutable(ctx, o.dir); err != nil {
		return nil, err
	}
	if command == "look" {
		f, err := suite.VerifyFreeze(o.dir, o.config)
		if err != nil {
			return nil, err
		}
		if err := suite.VerifyLiveProfile(o.config, f.Roles); err != nil {
			return nil, err
		}
	}
	store, err := storeFor(o)
	if err != nil {
		return nil, err
	}
	if command == "look" {
		return suite.Look(ctx, o.dir, o.config, bind, store, look, o.workers)
	}
	ss, err := suite.Scenarios(o.dir, o.config, "pilot")
	if err != nil {
		return nil, err
	}
	cfg := eval.Config{Harness: longrun.Harness(bind, fake.Faults{}), Scenarios: ss, Variants: scenarios.Variants()[:1], Repeats: 2, Concurrency: o.workers}
	exp, err := suite.RunExperiment(ctx, store, cfg, eval.ExperimentMeta{Label: "pilot/" + o.config})
	if err != nil {
		return nil, err
	}
	after, err := suite.ReadSource(ctx, o.dir, o.config)
	if err != nil {
		return nil, err
	}
	if after.SHA256 != source.SHA256 {
		return nil, fmt.Errorf("source changed during pilot")
	}
	if err := suite.VerifyLiveProfile(o.config, profile.Roles); err != nil {
		return nil, err
	}
	if _, err := suite.WritePilotEvidence(o.dir, o.config, exp, profile, source.Identity.SHA256); err != nil {
		return nil, err
	}
	return exp.Header, nil
}
func embedded(ctx context.Context, command string, args []string, stdout, stderr io.Writer) int {
	// The ordinary eval CLI owns selectors and its own flags. Its run command uses
	// only the disjoint smoke pool; frozen looks have a separate guarded command.
	ss, err := suite.Scenarios(defaultDir(), "offline", "smoke")
	if err != nil {
		return fail(stderr, err)
	}
	return evalcli.Main(ctx, append([]string{command}, args...), evalcli.Config{Name: "compaction-gate", Harness: longrun.Harness("offline", fake.Faults{}), Scenarios: ss, Variants: scenarios.Variants(), Stdout: stdout, Stderr: stderr})
}

func defaultDir() string {
	const fromRoot = "integration_tests/fixtures/longrun"
	if _, err := os.Stat(filepath.Join(fromRoot, "go.mod")); err == nil {
		return fromRoot
	}
	return "."
}
