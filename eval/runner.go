package eval

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
)

// Config declares an experiment. Zero Repeats and Concurrency both mean one.
// OnAttempt runs serially once per record; callbacks must not mutate configuration.
// A callback panic stops new attempts and disables subsequent callbacks.
type Config struct {
	Harness                         HarnessFunc
	Scenarios                       []Scenario
	Variants                        []Variant
	Select                          Selection
	Repeats, Concurrency, MaxTokens int
	OnAttempt                       func(*AttemptRecord)
	Judge                           OutcomeJudge
	// JudgeTimeout bounds judging per attempt, separately from Scenario.Timeout.
	// Zero uses five minutes; negative values are invalid.
	JudgeTimeout time.Duration
}

type runnerJob struct {
	index   int
	attempt plannedAttempt
}
type harnessOwners struct {
	mu   sync.Mutex
	seen map[*agenttest.Harness]bool
}

// Run executes a validated matrix with isolated harnesses and bounded concurrency.
// A stopped experiment retains one record per planned key. Only invalid config,
// caller cancellation, disabled live models and callback panics return errors;
// quality and harness failures remain in records. The first stop reason wins.
// Already running attempts finish after a token stop or callback panic.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	plan, err := buildPlan(cfg)
	if err != nil {
		return nil, err
	}
	report := newReport(cfg, plan)
	var prepareErr error
	if cfg.Judge != nil {
		prepareErr = cfg.Judge.Prepare(ctx)
	}
	concurrency := min(report.Concurrency, len(plan))
	jobs := make(chan runnerJob)
	completed := make(chan *AttemptRecord, concurrency)
	owners := &harnessOwners{seen: make(map[*agenttest.Harness]bool)}
	factory := cfg.Harness
	var workers sync.WaitGroup
	for range concurrency {
		workers.Go(func() {
			for job := range jobs {
				record := runAttempt(ctx, factory, owners, job)
				judgeAttempt(ctx, cfg.Judge, cfg.JudgeTimeout, record)
				completed <- record
			}
		})
	}
	order := schedule(plan)
	runErr := collect(ctx, &cfg, report, plan, order, jobs, completed)
	close(jobs)
	workers.Wait()
	for _, a := range report.Attempts {
		if a.Outcome == nil {
			a.NotRun = report.Stop
			runErr = notifyAttempt(&cfg, report, a, runErr)
		}
	}
	if cfg.Judge != nil {
		info := cfg.Judge.Info()
		if prepareErr != nil {
			info.Calibrated = false
		}
		report.Judge = &info
	}
	report.Finished = time.Now()
	return report, runErr
}

// collect owns dispatch, stop decisions, accounting and all callbacks.
func collect(ctx context.Context, cfg *Config, report *Report, plan []plannedAttempt, order []int, jobs chan<- runnerJob, completed <-chan *AttemptRecord) error {
	next, inFlight := 0, 0
	var runErr error
	canceled := ctx.Done()
	for next < len(order) || inFlight > 0 {
		if canceled != nil && ctx.Err() != nil {
			report.stop(StopCanceled)
			runErr = errors.Join(runErr, context.Cause(ctx))
			canceled = nil
		}
		if report.Stop != "" && inFlight == 0 {
			break
		}
		var dispatch chan<- runnerJob
		var job runnerJob
		if report.Stop == "" && next < len(order) && inFlight < report.Concurrency {
			dispatch = jobs
			index := order[next]
			job = runnerJob{index: index, attempt: plan[index]}
		}
		select {
		case <-canceled:
			report.stop(StopCanceled)
			runErr = errors.Join(runErr, context.Cause(ctx))
			canceled = nil
		case dispatch <- job:
			next++
			inFlight++
		case a := <-completed:
			inFlight--
			runErr = acceptRecord(cfg, report, a, runErr)
		}
	}
	// A cancellation delivered by the final callback must still return its cause.
	if canceled != nil && ctx.Err() != nil {
		report.stop(StopCanceled)
		return errors.Join(runErr, context.Cause(ctx))
	}
	return runErr
}

// acceptRecord accounts completed work before deciding whether more work may start.
func acceptRecord(cfg *Config, report *Report, a *AttemptRecord, runErr error) error {
	report.Attempts[a.Index] = a
	if a.NotRun != "" {
		// Canceled dispatches are delivered with the other skipped records last.
		return runErr
	}
	report.Tokens += a.Tokens + a.JudgeTokens
	if errors.Is(a.HarnessErr, agenttest.ErrLiveModelDisabled) {
		report.stop(StopLiveModelDisabled)
		runErr = errors.Join(runErr, a.HarnessErr)
	}
	if report.Stop == "" && report.MaxTokens > 0 && report.Tokens >= report.MaxTokens {
		report.Stop = StopTokenLimit
	}
	return notifyAttempt(cfg, report, a, runErr)
}

// notifyAttempt disables a panicking observer so collection can drain the workers.
func notifyAttempt(cfg *Config, report *Report, a *AttemptRecord, runErr error) (err error) {
	if cfg.OnAttempt == nil {
		return runErr
	}
	defer func() {
		if value := recover(); value != nil {
			cfg.OnAttempt = nil
			report.stop(StopCallbackPanic)
			panicErr := fmt.Errorf("eval: OnAttempt panic: %v\n%s", value, debug.Stack())
			a.HarnessErr = errors.Join(a.HarnessErr, panicErr)
			err = errors.Join(runErr, panicErr)
		}
	}()
	cfg.OnAttempt(a)
	return runErr
}

func schedule(plan []plannedAttempt) []int {
	order := make([]int, len(plan))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		x, y := plan[a].scenario.Timeout, plan[b].scenario.Timeout
		if x == y {
			return 0
		}
		if x == 0 {
			return -1
		}
		if y == 0 {
			return 1
		}
		if x > y {
			return -1
		}
		return 1
	})
	return order
}

func newReport(cfg Config, plan []plannedAttempt) *Report {
	variants := slices.Clone(cfg.Variants)
	if len(variants) == 0 {
		variants = []Variant{{ID: DefaultVariant}}
	}
	for i := range variants {
		variants[i].Labels = maps.Clone(variants[i].Labels)
		variants[i].Policy = clonePolicy(variants[i].Policy)
		variants[i].Options = slices.Clone(variants[i].Options)
	}
	r := &Report{Variants: variants, Selection: Selection{Variants: slices.Clone(cfg.Select.Variants), Scenarios: slices.Clone(cfg.Select.Scenarios), Tags: slices.Clone(cfg.Select.Tags), Items: slices.Clone(cfg.Select.Items), Keys: slices.Clone(cfg.Select.Keys)}, Repeats: cfg.Repeats, Concurrency: cfg.Concurrency, MaxTokens: cfg.MaxTokens, Started: time.Now(), Attempts: make([]*AttemptRecord, len(plan))}
	if r.Repeats == 0 {
		r.Repeats = 1
	}
	if r.Concurrency == 0 {
		r.Concurrency = 1
	}
	for i, a := range plan {
		r.Attempts[i] = &AttemptRecord{AttemptKey: a.key, Index: i}
	}
	for _, s := range cfg.Scenarios {
		info := ScenarioInfo{ID: s.ID, Agent: s.Agent, Tags: slices.Clone(s.Tags), Timeout: s.Timeout, DatasetSource: s.Items.Source(), DatasetHash: s.Items.Hash()}
		for _, item := range s.Items.items {
			if slices.ContainsFunc(plan, func(a plannedAttempt) bool {
				return a.scenario.ID == s.ID && a.item.ID == item.ID
			}) {
				info.Items = append(info.Items, item.ID)
			}
		}
		if len(info.Items) > 0 {
			r.Scenarios = append(r.Scenarios, info)
		}
	}
	return r
}

// runAttempt closes each newly owned harness exactly once, including error paths.
func runAttempt(ctx context.Context, factory HarnessFunc, owners *harnessOwners, job runnerJob) *AttemptRecord {
	a := job.attempt
	if ctx.Err() != nil {
		return &AttemptRecord{AttemptKey: a.key, Index: job.index, NotRun: StopCanceled}
	}
	r := &AttemptRecord{AttemptKey: a.key, Index: job.index, Outcome: &Outcome{Scenario: a.key.Scenario, Item: a.key.Item}, Started: time.Now()}
	defer func() {
		r.Duration = time.Since(r.Started)
	}()
	opts := append(slices.Clone(a.variant.Options), agenttest.WithDefaultRunOptions(variantRunOptions(a.variant)...))
	h, err := callFactory(ctx, factory, a.variant, opts)
	r.HarnessErr = err
	if h == nil {
		if err == nil {
			r.HarnessErr = errors.New("eval: factory returned a nil harness")
		}
		return r
	}
	if !owners.claim(h) {
		r.HarnessErr = errors.Join(err, errors.New("eval: factory reused a harness"))
		return r
	}
	defer func() {
		r.HarnessErr = errors.Join(r.HarnessErr, h.Close())
	}()
	if err != nil {
		return r
	}
	out, executeErr := Execute(ctx, h, a.scenario, a.item)
	if out != nil {
		r.Outcome = out
	}
	r.Outcome.Err = errors.Join(r.Outcome.Err, executeErr)
	r.countUsage()
	return r
}

func callFactory(ctx context.Context, factory HarnessFunc, v Variant, opts []agenttest.Option) (h *agenttest.Harness, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("eval: harness factory panic: %v\n%s", value, debug.Stack())
		}
	}()
	return factory(ctx, v, opts...)
}

func (o *harnessOwners) claim(h *agenttest.Harness) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.seen[h] {
		return false
	}
	o.seen[h] = true
	return true
}
