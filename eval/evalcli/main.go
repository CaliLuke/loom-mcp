// Package evalcli embeds a local experiment CLI in an application's eval program.
package evalcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/eval/summaryeval"
)

// Config supplies application scenarios, isolated harnesses and CLI destinations.
// Harness and Scenarios are required except for help. Empty Name means "evals".
// Empty StoreDir uses eval.DefaultStoreDir(Dir); empty Dir uses os.Getwd.
// Nil output writers use os.Stdout and os.Stderr.
type Config struct {
	// Compaction enables offline summary evaluation independently of Harness and Scenarios.
	Compaction *summaryeval.Config
	Name       string
	Harness    eval.HarnessFunc
	Scenarios  []eval.Scenario
	Variants   []eval.Variant
	// NewJudge optionally constructs the judge using the store's calibration cache.
	// --no-judge skips the factory; ErrLiveModelDisabled runs without judging.
	NewJudge       func(cache judge.Cache) (*judge.Judge, error)
	StoreDir       string
	Dir            string
	Stdout, Stderr io.Writer
}

type commandError struct {
	code int
	err  error
}

const (
	commandHelp    = "help"
	commandRun     = "run"
	commandShow    = "show"
	commandCompare = "compare"
	commandList    = "list"
)

// Main runs args (normally os.Args[1:]) without installing signal handlers.
// It returns 0 for completed commands regardless of verdicts, 1 for operational
// failures, and 2 for usage errors. The caller supplies cancellation through ctx.
func Main(ctx context.Context, args []string, cfg Config) int {
	if cfg.Name == "" {
		cfg.Name = "evals"
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	err := dispatch(ctx, args, cfg)
	if err == nil {
		return 0
	}
	code := 1
	var commandErr *commandError
	if errors.As(err, &commandErr) {
		code = commandErr.code
	}
	if _, writeErr := fmt.Fprintln(cfg.Stderr, err); writeErr != nil {
		return 1
	}
	return code
}

func dispatch(ctx context.Context, args []string, cfg Config) error {
	var common flags
	root := newFlags(cfg.Name, &common)
	if root.Parse(args) == nil {
		rest := root.Args()
		if len(rest) > 0 && rest[0] == "compaction" {
			return compactionCommand(ctx, rest[1:], cfg, common)
		}
		if len(rest) == 2 && rest[0] == commandHelp && rest[1] == "compaction" {
			return compactionCommand(ctx, nil, cfg, common)
		}
	}
	invocation, err := parseCommand(cfg.Name, args)
	if err != nil {
		return err
	}
	if invocation.help {
		return help(cfg, invocation.command)
	}
	if cfg.Harness == nil || len(cfg.Scenarios) == 0 {
		return usage(errors.New("Harness and Scenarios are required"))
	}
	command, f, positions := invocation.command, invocation.flags, invocation.positions
	if err := f.validate(command, positions); err != nil {
		return usage(err)
	}
	switch command {
	case commandRun:
		return run(ctx, cfg, f)
	case commandShow:
		return show(cfg, f, positions[0])
	case commandList:
		return list(cfg, f)
	case commandCompare:
		return compare(cfg, f, positions)
	default:
		panic("validated command")
	}
}

func usage(err error) error {
	return &commandError{code: 2, err: err}
}

func openStore(cfg Config, f flags, options ...eval.StoreOption) (*eval.Store, error) {
	dir, err := storeDir(cfg, f)
	if err != nil {
		return nil, err
	}
	return eval.Open(dir, options...)
}

func storeDir(cfg Config, f flags) (string, error) {
	dir := f.out
	if dir == "" {
		dir = cfg.StoreDir
	}
	if dir == "" {
		start := cfg.Dir
		if start == "" {
			var err error
			start, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		var err error
		dir, err = eval.DefaultStoreDir(start)
		if err != nil {
			var pathErr *os.PathError
			if errors.As(err, &pathErr) {
				return "", err
			}
			return "", usage(err)
		}
	}
	return dir, nil
}

func loadRef(store *eval.Store, ref string) (*eval.Experiment, error) {
	id, err := store.Resolve(ref)
	if err != nil {
		// Resolve also reads headers: malformed files and I/O are operational errors.
		var pathErr *os.PathError
		var decodeErr *eval.DecodeError
		if errors.As(err, &pathErr) || errors.As(err, &decodeErr) {
			return nil, err
		}
		return nil, usage(err)
	}
	return store.Load(id)
}

func (e *commandError) Error() string {
	return e.err.Error()
}

func (e *commandError) Unwrap() error {
	return e.err
}
