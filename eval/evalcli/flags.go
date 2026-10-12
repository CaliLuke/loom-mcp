package evalcli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

type flags struct {
	out                                    string
	json, full, export, quiet, mergeParent bool
	selection                              eval.Selection
	variants                               []string
	repeat, concurrency, maxTokens, limit  int
	repeatSet                              bool
	noJudge                                bool
	label, rerun, baseline                 string
}

type listValue []string

type invocation struct {
	command   string
	flags     flags
	positions []string
	help      bool
}

func parseCommand(name string, args []string) (*invocation, error) {
	result := &invocation{}
	root := newFlags(name, &result.flags)
	if err := root.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			result.help = true
			return result, nil
		}
		return nil, usage(err)
	}
	args = root.Args()
	if len(args) == 0 {
		return nil, usage(errors.New("expected run, show, compare, list or help"))
	}
	if args[0] == commandHelp {
		if len(args) > 2 {
			return nil, usage(errors.New("help accepts at most one command"))
		}
		result.help = true
		if len(args) == 2 {
			result.command = args[1]
		}
		return result, nil
	}
	result.command = args[0]
	fs, err := commandFlags(name, result.command, &result.flags)
	if err != nil {
		return nil, err
	}
	result.positions, err = parseAnywhere(fs, args[1:])
	if errors.Is(err, flag.ErrHelp) {
		result.help = true
		return result, nil
	}
	if err != nil {
		return nil, usage(err)
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "repeat" {
			result.flags.repeatSet = true
		}
	})
	return result, nil
}

func newFlags(name string, f *flags) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&f.out, "out", f.out, "experiment store directory")
	fs.BoolVar(&f.json, "json", f.json, "write JSON to stdout")
	return fs
}

func commandFlags(name, command string, f *flags) (*flag.FlagSet, error) {
	fs := newFlags(name+" "+command, f)
	switch command {
	case commandRun:
		fs.Var((*listValue)(&f.selection.Variants), "variant", "variant IDs (repeatable or comma-separated)")
		fs.Var((*listValue)(&f.selection.Scenarios), "scenario", "scenario IDs (repeatable or comma-separated)")
		fs.Var((*listValue)(&f.selection.Tags), "tag", "scenario tags (repeatable or comma-separated)")
		fs.Var((*listValue)(&f.selection.Items), "item", "item IDs or scenario/item (repeatable or comma-separated)")
		fs.IntVar(&f.repeat, "repeat", 0, "repeats per group (default 1, or parent's repeats)")
		fs.IntVar(&f.concurrency, "concurrency", 0, "parallel attempts (default 1)")
		fs.IntVar(&f.maxTokens, "max-tokens", 0, "stop starts at this token total (0 = unlimited)")
		fs.StringVar(&f.label, "label", "", "experiment label")
		fs.StringVar(&f.rerun, "rerun-failed", "", "rerun failed, error and not-run groups")
		fs.StringVar(&f.baseline, commandCompare, "", "compare with an experiment or last")
		fs.BoolVar(&f.mergeParent, "merge-parent", false, "overlay rerun groups on their parent for comparison")
		fs.BoolVar(&f.quiet, "quiet", false, "suppress attempt progress")
		fs.BoolVar(&f.noJudge, "no-judge", false, "skip judge creation and judging")
	case commandShow:
		fs.BoolVar(&f.full, "full", false, "render without trajectory limits")
		fs.BoolVar(&f.export, "export", false, "include raw exports in JSON")
	case commandCompare:
		fs.Var((*listValue)(&f.variants), "variants", "compare variants in one experiment (first is baseline)")
		fs.BoolVar(&f.mergeParent, "merge-parent", false, "overlay candidate rerun on its parent")
	case commandList:
		fs.IntVar(&f.limit, "limit", 20, "maximum experiments (0 = all)")
	default:
		return nil, usage(fmt.Errorf("unknown command %q", command))
	}
	return fs, nil
}

// parseAnywhere resumes flag parsing after each positional argument.
func parseAnywhere(fs *flag.FlagSet, args []string) ([]string, error) {
	var positions []string
	for len(args) > 0 {
		if args[0] == "--" {
			return append(positions, args[1:]...), nil
		}
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		// An explicit terminator disables subsequent flag parsing.
		consumed := len(args) - len(rest)
		if consumed > 0 && args[consumed-1] == "--" {
			return append(positions, rest...), nil
		}
		positions = append(positions, rest[0])
		args = rest[1:]
	}
	return positions, nil
}

func help(cfg Config, command string) error {
	if command == "" {
		_, err := fmt.Fprintf(cfg.Stdout, "Usage: %s <run|show|compare|list|compaction|help> [flags]\nUse %s help <command> for flags. --out and --json apply to every command.\n", cfg.Name, cfg.Name)
		return err
	}
	var f flags
	fs, err := commandFlags(cfg.Name, command, &f)
	if err != nil {
		return err
	}
	suffix := map[string]string{commandRun: "", commandShow: " <experiment>[/<attempt>]", commandCompare: " <base> <candidate> | <experiment> --variants a,b[,c]", commandList: ""}[command]
	var out strings.Builder
	fmt.Fprintf(&out, "Usage: %s %s%s [flags]\n", cfg.Name, command, suffix)
	fs.SetOutput(&out)
	fs.PrintDefaults()
	_, err = io.WriteString(cfg.Stdout, out.String())
	return err
}

func (v *listValue) String() string {
	return strings.Join(*v, ",")
}

func (v *listValue) Set(value string) error {
	*v = append(*v, strings.Split(value, ",")...)
	return nil
}

func (f flags) validate(command string, positions []string) error {
	count := len(positions)
	switch command {
	case commandRun:
		if f.repeat < 0 || f.concurrency < 0 || f.maxTokens < 0 {
			return errors.New("repeat, concurrency and max-tokens must be non-negative")
		}
		if count != 0 {
			return errors.New("run accepts flags only")
		}
		if f.mergeParent && (f.rerun == "" || f.baseline == "") {
			return errors.New("run --merge-parent requires --rerun-failed and --compare")
		}
		if f.rerun != "" && (len(f.selection.Variants)+len(f.selection.Scenarios)+len(f.selection.Tags)+len(f.selection.Items) > 0) {
			return errors.New("selection flags cannot be combined with --rerun-failed")
		}
		if !utf8.ValidString(f.label) || len(f.label) > 256 {
			return errors.New("label must be valid UTF-8 and at most 256 bytes")
		}
	case commandShow:
		if count != 1 {
			return errors.New("show requires one experiment[/attempt]")
		}
	case commandList:
		if count != 0 || f.limit < 0 {
			return errors.New("list accepts no arguments and requires a non-negative limit")
		}
	case commandCompare:
		if len(f.variants) > 0 {
			if count != 1 || len(f.variants) < 2 || f.mergeParent {
				return errors.New("compare --variants requires one experiment, at least two variants and no --merge-parent")
			}
		} else if count != 2 {
			return errors.New("compare requires baseline and candidate experiments")
		}
	}
	return nil
}
