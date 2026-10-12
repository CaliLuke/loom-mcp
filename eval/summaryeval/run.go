package summaryeval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	agRuntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/google/uuid"
)

type work struct {
	cut   Cut
	in    *agRuntime.SummarizeContextInput
	model Model
	line  Line
}

// Run verifies every baseline and plans every variant before making provider calls.
func Run(ctx context.Context, cfg Config, opts Options) (*Experiment, error) {
	var err error
	opts, err = normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	models, disabled, err := newModels(cfg, opts.Notice)
	if err != nil {
		return nil, err
	}
	result := &Experiment{Header: Header{Format: replayFormat, Version: 1, Label: opts.Label, Status: statusRunning, Started: opts.Now(), Source: opts.Source, Identity: eval.DetectIdentity(ctx, opts.Dir)}}
	tasks, err := prepare(ctx, opts, models, disabled, result)
	if err != nil {
		return nil, err
	}
	comparisons, err := prepareComparisons(ctx, cfg, opts, tasks, result)
	if err != nil {
		return nil, err
	}
	if opts.OnPreflight != nil {
		if err := opts.OnPreflight(result.Preflight); err != nil {
			return nil, err
		}
	}
	if opts.DryRun {
		return result, nil
	}
	id, err := opts.NewID()
	if err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, fmt.Errorf("invalid replay ID")
	}
	result.Header.ID = id
	w, err := begin(opts.Dir, result.Header)
	if err != nil {
		return nil, err
	}
	runErr := execute(ctx, opts, tasks, result, w, comparisons)
	finishResult(result, opts.Now(), runErr)
	return result, errors.Join(runErr, w.finish(result.Header))
}

func prepare(ctx context.Context, opts Options, models map[string]Model, disabled bool, result *Experiment) ([]work, error) {
	var tasks []work
	result.Preflight.Cuts = len(opts.Cuts)
	identities := map[string]bool{}
	for _, cut := range opts.Cuts {
		next, err := prepareCut(ctx, opts, models, disabled, result, identities, cut)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, next...)
	}
	result.Header.Planned = len(tasks)
	return tasks, nil
}

func execute(ctx context.Context, opts Options, tasks []work, result *Experiment, w *writer, comparisons map[cutLocator]*comparison) error {
	var errs []error
	for start := 0; start < len(tasks); start += opts.Concurrency {
		batch := tasks[start:min(start+opts.Concurrency, len(tasks))]
		lines, failures := runBatch(ctx, opts, batch, result.Header.Tokens)
		for i, line := range lines {
			if failures[i] != nil {
				line.Error = failures[i].Error()
				errs = append(errs, failures[i])
			} else if line.NotRun == "" && line.Error == "" {
				result.Header.Tokens += tokenCount(line.New.Usage)
				if !line.New.Cached {
					result.Header.Calls += len(line.New.Calls)
				}
			}
			if opts.Judge != nil && line.NotRun == "" && line.Error == "" {
				if c := comparisons[cutKey(line.Cut)]; c != nil {
					if err := applyJudgments(ctx, opts.Judge, c, &line, &result.Header); err != nil {
						line.Error = err.Error()
						errs = append(errs, err)
					}
				}
			}
			result.Lines = append(result.Lines, line)
			if err := w.append(line); err != nil {
				return errors.Join(errors.Join(errs...), err)
			}
		}
	}
	return errors.Join(append(errs, ctx.Err())...)
}

func runWork(ctx context.Context, opts Options, task work) (Side, error) {
	path, key, err := cachePath(opts.Dir, task.cut, task.in, task.line.Repeat)
	if err != nil {
		return Side{}, err
	}
	if !opts.Fresh {
		if side, ok := cached(path, key); ok {
			return side, nil
		}
	}
	side, err := replay(ctx, task.cut, task.in, task.model.Client, opts.Now)
	if err != nil {
		return side, err
	}
	return side, atomicJSON(path, cachedSide{Schema: cacheSchema, Key: key, Side: side})
}

func normalizeOptions(opts Options) (Options, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewID == nil {
		opts.NewID = func() (string, error) {
			id, err := uuid.NewV7()
			return id.String(), err
		}
	}
	if opts.Notice == nil {
		opts.Notice = io.Discard
	}
	if opts.Repeat == 0 {
		opts.Repeat = 1
	}
	if opts.Concurrency == 0 {
		opts.Concurrency = 1
	}
	if opts.Repeat < 1 || opts.Concurrency < 1 || opts.MaxTokens < 0 || len(opts.Variants) == 0 {
		return opts, fmt.Errorf("%w: positive repeats/concurrency and variants required", ErrUsage)
	}
	return opts, nil
}

func newModels(cfg Config, notice io.Writer) (map[string]Model, bool, error) {
	models := map[string]Model{}
	disabled := false
	if cfg.NewModels != nil {
		var err error
		models, err = cfg.NewModels()
		if errors.Is(err, agenttest.ErrLiveModelDisabled) {
			disabled = true
			if _, err := fmt.Fprintln(notice, "compaction models disabled: set LOOM_LIVE_MODEL=1 to enable live summaries"); err != nil {
				return nil, false, err
			}
		} else if err != nil {
			return nil, false, err
		}
	}
	return models, disabled, nil
}

func prepareCut(ctx context.Context, opts Options, models map[string]Model, disabled bool, result *Experiment, identities map[string]bool, cut Cut) ([]work, error) {
	var tasks []work
	cut, baseline, verification, err := prepareBaseline(ctx, cut)
	if err != nil {
		return nil, err
	}
	for _, v := range opts.Variants {
		current := cut
		var in *agRuntime.SummarizeContextInput
		var m Model
		chunks, tokens := 0, 0
		if baseline != nil {
			var identity VariantIdentity
			current, in, m, identity, err = variantInput(cut, baseline, v, models, disabled)
			if err != nil {
				return nil, err
			}
			if !disabled {
				chunks, tokens, err = plan(ctx, current, in, m)
				if err != nil {
					return nil, err
				}
			}
			key := identity.ID + identity.Fingerprint
			if !identities[key] {
				identities[key] = true
				result.Header.Variants = append(result.Header.Variants, identity)
			}
		}
		for repeat := 1; repeat <= opts.Repeat; repeat++ {
			line := Line{Key: fmt.Sprintf("%s:%d/%s/%d", cut.RunID, cut.Step, v.ID, repeat), Variant: v.ID, Repeat: repeat, Cut: cut, Old: cut.Old, Verification: verification}
			if disabled {
				line.NotRun = reasonLiveDisabled
			} else if baseline == nil {
				line.Error = reasonViewMismatch
			}
			tasks = append(tasks, work{cut: current, in: in, model: m, line: line})
			result.Preflight.CallUpperBound += 3 * chunks
			result.Preflight.InputTokens += tokens
		}
	}
	return tasks, nil
}

func runBatch(ctx context.Context, opts Options, batch []work, tokens int) ([]Line, []error) {
	lines := make([]Line, len(batch))
	failures := make([]error, len(batch))
	var wg sync.WaitGroup
	for i, task := range batch {
		lines[i] = task.line
		if ctx.Err() != nil {
			lines[i].NotRun = reasonCanceled
		} else if opts.MaxTokens > 0 && tokens >= opts.MaxTokens {
			lines[i].NotRun = reasonTokenLimit
		}
		if lines[i].NotRun != "" || lines[i].Error != "" {
			continue
		}
		wg.Go(func() {
			lines[i].New, failures[i] = runWork(ctx, opts, task)
		})
	}
	wg.Wait()
	return lines, failures
}

func finishResult(result *Experiment, finished time.Time, runErr error) {
	result.Header.Finished = finished
	result.Header.Status = statusComplete
	if runErr != nil {
		result.Header.Status = statusStopped
	}
	for _, line := range result.Lines {
		if line.NotRun == reasonTokenLimit || line.NotRun == reasonCanceled {
			result.Header.Status = statusStopped
		}
	}
}

func prepareBaseline(ctx context.Context, cut Cut) (Cut, *agRuntime.SummarizeContextInput, Verification, error) {
	verification, err := Verify(ctx, cut)
	if err != nil {
		return cut, nil, verification, err
	}
	baseline, err := Reconstruct(cut)
	if err != nil && !errors.Is(err, errViewMismatch) {
		return cut, nil, verification, err
	}
	if baseline != nil {
		cut.Old, err = oldSide(ctx, cut, baseline)
		if err != nil {
			return cut, nil, verification, err
		}
	}
	return cut, baseline, verification, nil
}
