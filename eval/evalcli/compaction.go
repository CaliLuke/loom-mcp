package evalcli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/eval/summaryeval"
)

type compactionFlags struct {
	common     flags
	selection  summaryeval.Selection
	variants   rawValues
	cuts       string
	fresh, dry bool
}

type rawValues []string

func (v *rawValues) String() string {
	return strings.Join(*v, ";")
}

func (v *rawValues) Set(s string) error {
	*v = append(*v, s)
	return nil
}

func compactionCommand(ctx context.Context, args []string, cfg Config, common flags) error {
	if len(args) == 0 || args[0] == commandHelp || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintf(cfg.Stdout, "Usage: %s compaction <run|show|list> [flags]\nrun --from <experiment>|--export <file> --variant prompt=<name|@path|recorded>\n", cfg.Name)
		return err
	}
	command := args[0]
	f := compactionFlags{common: common}
	fs := newFlags(cfg.Name+" compaction "+command, &f.common)
	fs, err := compactionFlagSet(command, fs, &f)
	if err != nil {
		return err
	}
	positions, err := parseAnywhere(fs, args[1:])
	if errors.Is(err, flag.ErrHelp) {
		fs.SetOutput(cfg.Stdout)
		fs.PrintDefaults()
		return nil
	}
	if err != nil {
		return usage(err)
	}
	if cfg.Compaction == nil {
		return usage(errors.New("Compaction config is required"))
	}
	if (command == commandShow && len(positions) != 1) || (command != commandShow && len(positions) != 0) {
		return usage(errors.New("invalid compaction command arguments"))
	}
	err = dispatchCompaction(ctx, cfg, command, f, positions)
	if errors.Is(err, summaryeval.ErrUsage) {
		return usage(err)
	}
	return err
}

func dispatchCompaction(ctx context.Context, cfg Config, command string, f compactionFlags, positions []string) error {
	variants, err := compactionVariants(cfg, command, f)
	if err != nil {
		return err
	}
	dir, err := storeDir(cfg, f.common)
	if err != nil {
		return err
	}
	switch command {
	case commandRun:
		return compactionRun(ctx, cfg, f, dir, variants)
	case commandShow:
		id, err := summaryeval.Resolve(dir, positions[0])
		if err != nil {
			return err
		}
		e, err := summaryeval.Load(dir, id)
		if err != nil {
			return err
		}
		return compactionOutput(cfg, f, e)
	case commandList:
		return compactionList(cfg, f, dir)
	}
	return nil
}

func compactionRun(ctx context.Context, cfg Config, f compactionFlags, dir string, variants []summaryeval.Variant) error {
	f.selection.All = f.cuts == "all"
	cuts, source, err := summaryeval.Read(dir, f.selection)
	if err != nil {
		return err
	}
	for _, cut := range cuts {
		if cut.Origin == "receipt_only" {
			for _, v := range variants {
				if v.Model == "" {
					return usage(errors.New("receipt_only cuts require a variant model"))
				}
			}
		}
	}
	j, err := compactionJudge(cfg, f, dir)
	if err != nil {
		return err
	}
	e, err := summaryeval.Run(ctx, *cfg.Compaction, summaryeval.Options{Dir: dir, Source: source, Cuts: cuts, Variants: variants, Repeat: f.common.repeat, Concurrency: f.common.concurrency, MaxTokens: f.common.maxTokens, Label: f.common.label, DryRun: f.dry, Fresh: f.fresh, Judge: j, Notice: cfg.Stderr, OnPreflight: func(p summaryeval.Preflight) error {
		_, err := fmt.Fprintf(cfg.Stderr, "Preflight: %d cuts, <= %d calls, ~%d input tokens\n", p.Cuts, p.CallUpperBound, p.InputTokens)
		return err
	}})
	if e != nil {
		if writeErr := compactionOutput(cfg, f, e); writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	return err
}

func compactionOutput(cfg Config, f compactionFlags, e *summaryeval.Experiment) error {
	if f.common.json {
		b, err := summaryeval.ShowJSON(e)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cfg.Stdout, string(b))
		return err
	}
	return summaryeval.Show(cfg.Stdout, e, f.common.full)
}

func compactionFlagSet(command string, fs *flag.FlagSet, f *compactionFlags) (*flag.FlagSet, error) {
	switch command {
	case commandRun:
		fs.StringVar(&f.selection.From, "from", "", "recorded experiment, prefix or last")
		fs.StringVar(&f.selection.Export, "export", "", "raw runrecord export file")
		fs.Var((*rawValues)(&f.selection.Attempts), "attempt", "T13 attempt reference (repeatable)")
		fs.StringVar(&f.selection.Run, "run", "", "run ID")
		fs.Var(&f.variants, "variant", "key=value[,key=value] (repeatable)")
		fs.StringVar(&f.cuts, "cuts", "summary", "summary or all")
		fs.IntVar(&f.selection.Step, "step", 0, "planner step")
		fs.IntVar(&f.selection.MaxCuts, "max-cuts", 0, "maximum cuts")
		fs.IntVar(&f.common.maxTokens, "max-tokens", 0, "stop new work at total token usage")
		fs.IntVar(&f.common.repeat, "repeat", 1, "replays per variant")
		fs.IntVar(&f.common.concurrency, "concurrency", 1, "maximum concurrent cuts")
		fs.StringVar(&f.common.label, "label", "", "replay label")
		fs.BoolVar(&f.common.noJudge, "no-judge", false, "skip judging")
		fs.BoolVar(&f.dry, "dry-run", false, "preflight without provider calls")
		fs.BoolVar(&f.fresh, "fresh", false, "skip summary cache reads")
	case commandShow:
		fs.BoolVar(&f.common.full, "full", false, "print complete summaries")
	case commandList:
		fs.IntVar(&f.common.limit, "limit", 20, "maximum replays (0 = all)")
	default:
		return nil, usage(fmt.Errorf("unknown compaction command %q", command))
	}
	return fs, nil
}

func compactionVariants(cfg Config, command string, f compactionFlags) ([]summaryeval.Variant, error) {
	var variants []summaryeval.Variant
	if command == commandRun {
		if (f.selection.From == "") == (f.selection.Export == "") || f.common.repeat < 1 || f.common.concurrency < 1 || f.common.maxTokens < 0 || (f.cuts != "all" && f.cuts != "summary") {
			return nil, usage(errors.New("exactly one of --from/--export and valid positive run limits are required"))
		}
		if len(f.variants) == 0 {
			return nil, usage(errors.New("at least one --variant is required"))
		}
		for _, spec := range f.variants {
			v, err := summaryeval.ParseVariant(*cfg.Compaction, spec)
			if err != nil {
				return nil, err
			}
			variants = append(variants, v)
		}
	}
	return variants, nil
}

func compactionList(cfg Config, f compactionFlags, dir string) error {
	if f.common.limit < 0 {
		return usage(errors.New("limit must be nonnegative"))
	}
	headers, err := summaryeval.List(dir)
	if err != nil {
		return err
	}
	if f.common.limit > 0 && len(headers) > f.common.limit {
		headers = headers[:f.common.limit]
	}
	if f.common.json {
		b, err := summaryeval.ListJSON(headers)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cfg.Stdout, string(b))
		return err
	}
	for _, h := range headers {
		if _, err := fmt.Fprintf(cfg.Stdout, "%s\t%s\t%s\t%d tokens\n", h.ID, h.Status, h.Label, h.Tokens); err != nil {
			return err
		}
	}
	return nil
}

func compactionJudge(cfg Config, f compactionFlags, dir string) (*judge.Judge, error) {
	var j *judge.Judge
	if !f.common.noJudge && cfg.NewJudge != nil {
		cache, err := judge.NewDirCache(filepath.Join(dir, "_calibration"))
		if err != nil {
			return nil, err
		}
		j, err = cfg.NewJudge(cache)
		if errors.Is(err, agenttest.ErrLiveModelDisabled) {
			if _, err := fmt.Fprintln(cfg.Stderr, "compaction judge disabled: set LOOM_LIVE_MODEL=1 to enable live judging"); err != nil {
				return nil, err
			}
			j = nil
		} else if err != nil {
			return nil, err
		}
	}
	return j, nil
}
