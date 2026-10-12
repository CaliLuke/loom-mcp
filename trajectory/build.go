package trajectory

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	builder struct {
		record *runrecord.RunRecord
		runs   map[string]*runrecord.Run
		active map[string]bool
	}

	runBuilder struct {
		owner     *builder
		run       *runrecord.Run
		out       *Trajectory
		groups    map[int]*stepGroup
		tools     map[string]*ToolStep
		results   map[string]bool
		adopted   map[runstore.CallKey]bool
		callSteps map[runstore.CallKey]int
		opts      Options
		position  int
	}

	stepGroup struct {
		models  []Step
		entries []Step
		events  []Step
	}
)

// Build derives a sealed run and its linked children from rec alone, without store access.
// It rejects inconsistent call/result joins and missing child records. Output
// overrides only the requested run's final answer; child answers come from records.
func Build(rec *runrecord.RunRecord, runID string, opts Options) (*Trajectory, error) {
	if rec == nil || runID == "" {
		return nil, fmt.Errorf("trajectory: record and run ID are required")
	}
	b := &builder{record: rec, runs: make(map[string]*runrecord.Run, len(rec.Runs)), active: make(map[string]bool)}
	for i := range rec.Runs {
		run := &rec.Runs[i]
		b.runs[run.RunID] = run
	}
	return b.buildRun(runID, opts)
}

// buildRun guards recursion while preserving the owning call's child-link order.
func (b *builder) buildRun(runID string, opts Options) (*Trajectory, error) {
	run, ok := b.runs[runID]
	if !ok {
		return nil, fmt.Errorf("trajectory: missing run %q", runID)
	}
	if !run.Sealed {
		return nil, fmt.Errorf("%w: %s", ErrNotTerminal, runID)
	}
	if b.active[runID] {
		return nil, fmt.Errorf("trajectory: cyclic child link to %s", runID)
	}
	b.active[runID] = true
	defer delete(b.active, runID)
	r := &runBuilder{
		owner: b, run: run, opts: opts,
		out:    &Trajectory{RunID: runID, Pos: run.Pos, Prev: run.Prev, Totals: Totals{TokensByModel: make(map[string]model.TokenUsage), ModelCallsByClass: make(map[runrecord.CallClass]int)}},
		groups: make(map[int]*stepGroup), tools: make(map[string]*ToolStep), results: make(map[string]bool), adopted: make(map[runstore.CallKey]bool), callSteps: make(map[runstore.CallKey]int),
	}
	if err := r.readModels(); err != nil {
		return nil, err
	}
	if err := r.readEventsAndEntries(); err != nil {
		return nil, fmt.Errorf("trajectory %s: %w", runID, err)
	}
	if err := r.linkChildren(); err != nil {
		return nil, err
	}
	if err := r.totalTools(); err != nil {
		return nil, err
	}
	if opts.Output != nil {
		r.out.Final = opts.Output.Final
	}
	r.flatten()
	return r.out, nil
}

func (r *runBuilder) group(step int) *stepGroup {
	group := r.groups[step]
	if group == nil {
		group = &stepGroup{}
		r.groups[step] = group
	}
	return group
}

// flatten orders each step's model calls before its produced entries and observations.
func (r *runBuilder) flatten() {
	steps := make([]int, 0, len(r.groups))
	for step := range r.groups {
		steps = append(steps, step)
	}
	slices.Sort(steps)
	for _, step := range steps {
		group := r.groups[step]
		slices.SortStableFunc(group.models, func(a, b Step) int {
			if order := cmp.Compare(a.Model.Key.Seq, b.Model.Key.Seq); order != 0 {
				return order
			}
			return cmp.Compare(a.Model.Key.Attempt, b.Model.Key.Attempt)
		})
		r.out.Steps = append(r.out.Steps, group.models...)
		r.out.Steps = append(r.out.Steps, group.entries...)
		r.out.Steps = append(r.out.Steps, group.events...)
	}
}
