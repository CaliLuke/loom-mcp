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
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

type options struct {
	dir, config, out, pilot string
	offline, verify         bool
	seed                    uint64
	workers                 int
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
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
	flags.BoolVar(&o.offline, "offline", false, "use Reader under the selected configuration")
	flags.BoolVar(&o.verify, "verify", false, "recompute stored measures from exports")
	flags.Uint64Var(&o.seed, "seed", 19, "pool generation seed")
	flags.IntVar(&o.workers, "workers", 10, "parallel attempts or calibration cells")
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
	if _, err := longrun.ProviderConfiguration(o.config, fake.Faults{}); err != nil {
		return fail(stderr, err)
	}
	var result any
	var err error
	switch command {
	case "items":
		err = suite.WriteItems(o.dir, o.seed)
	case "validate":
		result, err = validate(ctx, o, stderr)
	case "calibrate":
		err = calibrate(ctx, o)
	case "freeze":
		result, err = freeze(o)
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
	v, runErr := suite.Validate(ctx, o.dir, o.workers, progress)
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return v, err
	}
	return v, errors.Join(runErr, os.WriteFile(filepath.Join(o.dir, "gate", "validation.json"), append(data, '\n'), 0600))
}
func calibrate(ctx context.Context, o options) error {
	v, err := suite.ReadValidation(o.dir)
	if err != nil {
		return err
	}
	cfg := suite.CalibrationConfig(o.config, v, o.workers)
	if o.pilot != "" {
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
		cfg.Pilot, err = suite.PilotRates(exp, o.config)
		if err != nil {
			return err
		}
	}
	return suite.Calibrate(ctx, o.dir, o.config, cfg)
}
func freeze(o options) (gate.Freeze, error) {
	data, err := os.ReadFile(suite.CalibrationPath(o.dir, o.config))
	if err != nil {
		return gate.Freeze{}, err
	}
	var artifact suite.CalibrationArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return gate.Freeze{}, err
	}
	if len(artifact.Report.Cells) != 972 || artifact.Report.Version != gate.SimulatorVersion {
		return gate.Freeze{}, fmt.Errorf("freeze requires the full production calibration")
	}
	identity := eval.DetectIdentity(context.Background(), o.dir)
	f, err := suite.BuildFreeze(o.dir, o.config, data, identity.Source)
	if err != nil {
		return f, err
	}
	return f, suite.WriteFreeze(o.dir, f)
}
func experiment(ctx context.Context, command string, look int, o options) (any, error) {
	store, err := storeFor(o)
	if err != nil {
		return nil, err
	}
	if command == "report" {
		return suite.StoredReport(o.dir, o.config, store, o.verify)
	}
	bind, err := binding(o)
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
