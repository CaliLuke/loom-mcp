package gate

import (
	"fmt"

	"slices"
	"sort"
)

type itemDelta struct {
	values [][3]float64
	counts []int
}
type population struct {
	scenarios [][]itemDelta
	missing   []Key
	rows      Table
}

// Evaluate computes all measures with one shared stratified item draw per
// bootstrap resample, across both variants and every paired measure.
func Evaluate(table Table, cfg Config, look int) (Evaluation, error) {
	return evaluate(table, cfg, look, 10000)
}
func evaluate(table Table, cfg Config, look, b int) (Evaluation, error) {
	if err := validateConfig(cfg, look); err != nil {
		return Evaluation{}, err
	}
	pop, err := pair(table, cfg)
	if err != nil {
		return Evaluation{}, err
	}
	alpha := cfg.Policy.Alpha / float64(len(cfg.Policy.Looks))
	result := Evaluation{Policy: clonePolicy(cfg.Policy), Baseline: cfg.Baseline, Look: look, Missing: pop.missing, Variants: make([]VariantResult, len(cfg.Variants))}
	point := averages(pop.scenarios, len(cfg.Variants))
	samples, scenarioSamples := bootstrapIntervals(pop.scenarios, len(cfg.Variants), cfg.Policy.Bootstrap.Seed, look, b)
	lo, hi := PercentileIndices(b, alpha)
	for v, id := range cfg.Variants {
		intervals := [3]Interval{}
		for m := range 3 {
			slices.Sort(samples[v][m])
			intervals[m] = Interval{point[v][m], samples[v][m][lo], samples[v][m][hi]}
		}
		r := VariantResult{Variant: id, Success: Criterion(intervals[0], cfg.Policy.Success, true), Unsupported: unsupportedCriterion(intervals[1], cfg.Policy, id), Evidence: Criterion(intervals[2], cfg.Policy.Evidence, true), Recall: Recall(pop.rows, id, cfg.Scenarios, alpha), Scenarios: map[string]float64{}, ScenarioIntervals: map[string]Interval{}}
		for s, items := range pop.scenarios {
			mean := scenarioMean(items, v, 0)
			r.Scenarios[cfg.Scenarios[s]] = mean
			draws := scenarioSamples[v][s]
			slices.Sort(draws)
			r.ScenarioIntervals[cfg.Scenarios[s]] = Interval{Estimate: mean, Lo: draws[lo], Hi: draws[hi]}
			if draws[hi] < cfg.Policy.Guard {
				r.GuardFailures = append(r.GuardFailures, cfg.Scenarios[s])
				result.GuardFailures = append(result.GuardFailures, id+"/"+cfg.Scenarios[s])
			}
		}
		for _, key := range pop.missing {
			if key.Variant == cfg.Baseline || key.Variant == id {
				r.Missing = append(r.Missing, key)
			}
		}
		result.Variants[v] = r
	}
	return result, nil
}
func validateConfig(c Config, look int) error {
	if err := c.Policy.Validate(c.Variants); err != nil {
		return err
	}
	if look < 1 || look > 3 || c.Repeats != 2 || c.Baseline == "" || len(c.Variants) < 1 || len(c.Variants) > 2 || len(c.Scenarios) == 0 {
		return fmt.Errorf("gate: invalid look or configuration (one or two variants and two repeats required)")
	}
	for _, list := range [][]string{append([]string{c.Baseline}, c.Variants...), c.Scenarios} {
		seen := map[string]bool{}
		for _, s := range list {
			if s == "" || seen[s] {
				return fmt.Errorf("gate: empty or duplicate identity %q", s)
			}
			seen[s] = true
		}
	}
	return nil
}
func pair(table Table, c Config) (population, error) {
	byKey, items, err := indexRows(table, c)
	if err != nil {
		return population{}, err
	}
	p := population{scenarios: make([][]itemDelta, len(c.Scenarios))}
	missing := map[Key]bool{}
	for si, s := range c.Scenarios {
		ids := make([]string, 0, len(items[s]))
		for id := range items[s] {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if len(ids) == 0 {
			return population{}, fmt.Errorf("gate: scenario %s has no planned items", s)
		}
		for _, id := range ids {
			p.scenarios[si] = append(p.scenarios[si], pairedItem(&p, byKey, missing, c, s, id))
		}
	}
	for k := range missing {
		p.missing = append(p.missing, k)
	}
	sort.Slice(p.missing, func(i, j int) bool {
		a, b := p.missing[i], p.missing[j]
		if a.Scenario != b.Scenario {
			return a.Scenario < b.Scenario
		}
		if a.Item != b.Item {
			return a.Item < b.Item
		}
		if a.Repeat != b.Repeat {
			return a.Repeat < b.Repeat
		}
		return a.Variant < b.Variant
	})
	return p, nil
}
func addPairs(p *population, d *itemDelta, rows map[Key]Row, missing map[Key]bool, c Config, s, id string, rep int) {
	bk := Key{c.Baseline, s, id, rep}
	base, bok := rows[bk]
	bok = bok && !base.Infrastructure
	if !bok {
		missing[bk] = true
	}
	for v, variant := range c.Variants {
		key := Key{variant, s, id, rep}
		r, ok := rows[key]
		ok = ok && !r.Infrastructure
		if !ok {
			missing[key] = true
		}
		if !bok || !ok {
			continue
		}
		d.counts[v]++
		d.values[v][0] += binary(r.Success && !r.RunFailed) - binary(base.Success && !base.RunFailed)
		d.values[v][1] += binary(r.Unsupported) - binary(base.Unsupported)
		d.values[v][2] += binary(r.Evidence) - binary(base.Evidence)
		p.rows = append(p.rows, r)
	}
}
func scenarioMean(items []itemDelta, v, m int) float64 {
	sum, n := 0.0, 0
	for _, d := range items {
		if d.counts[v] > 0 {
			sum += d.values[v][m]
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
func averages(scenarios [][]itemDelta, variants int) [][3]float64 {
	out := make([][3]float64, variants)
	for _, items := range scenarios {
		for v := range variants {
			for m := range 3 {
				out[v][m] += scenarioMean(items, v, m) / float64(len(scenarios))
			}
		}
	}
	return out
}
func bootstrapIntervals(scenarios [][]itemDelta, variants int, seed uint64, look, b int) ([][3][]float64, [][][]float64) {
	out, scenarioSamples := allocateBootstrapSamples(len(scenarios), variants, b)
	rng := newSimulationRandom(seed, []uint64{0, 1, 2, 3}[look])
	for i := range b {
		for si, items := range scenarios {
			// Configuration validation limits production evaluation to two variants.
			var storage [2]struct {
				sums  [3]float64
				count int
			}
			accumulators := storage[:variants]
			for range items {
				d := items[rng.IntN(len(items))]
				for v := range accumulators {
					acc := &accumulators[v]
					if d.counts[v] > 0 {
						acc.count++
						for m := range acc.sums {
							acc.sums[m] += d.values[v][m]
						}
					}
				}
			}
			for v, acc := range accumulators {
				if acc.count > 0 {
					scenarioSamples[v][si][i] = acc.sums[0] / float64(acc.count)
					for m, sum := range acc.sums {
						out[v][m][i] += sum / float64(acc.count*len(scenarios))
					}
				}
			}
		}
	}
	return out, scenarioSamples
}

func indexRows(table Table, c Config) (map[Key]Row, map[string]map[string]bool, error) {
	byKey := map[Key]Row{}
	items := map[string]map[string]bool{}
	for _, s := range c.Scenarios {
		items[s] = map[string]bool{}
		for _, id := range c.Items[s] {
			items[s][id] = true
		}
	}
	for _, r := range table {
		if !slices.Contains(c.Scenarios, r.Scenario) || (r.Variant != c.Baseline && !slices.Contains(c.Variants, r.Variant)) {
			continue
		}
		if r.Item == "" || r.Repeat < 1 || r.Repeat > c.Repeats {
			return nil, nil, fmt.Errorf("gate: invalid row key %+v", r.Key)
		}
		if _, ok := byKey[r.Key]; ok {
			return nil, nil, fmt.Errorf("gate: duplicate key %+v", r.Key)
		}
		byKey[r.Key] = r
		if c.Items != nil && !items[r.Scenario][r.Item] {
			return nil, nil, fmt.Errorf("gate: item outside manifest: %+v", r.Key)
		}
		items[r.Scenario][r.Item] = true
	}
	return byKey, items, nil
}

func pairedItem(p *population, rows map[Key]Row, missing map[Key]bool, c Config, s, id string) itemDelta {
	d := itemDelta{values: make([][3]float64, len(c.Variants)), counts: make([]int, len(c.Variants))}
	for rep := 1; rep <= c.Repeats; rep++ {
		addPairs(p, &d, rows, missing, c, s, id, rep)
	}
	for v := range d.values {
		if d.counts[v] > 0 {
			for m := range 3 {
				d.values[v][m] /= float64(d.counts[v])
			}
		}
	}
	return d
}

// allocateBootstrapSamples allocates pooled and scenario intervals for shared draws.
func allocateBootstrapSamples(scenarios, variants, b int) ([][3][]float64, [][][]float64) {
	scenarioSamples := make([][][]float64, variants)
	for v := range variants {
		scenarioSamples[v] = make([][]float64, scenarios)
		for s := range scenarios {
			scenarioSamples[v][s] = make([]float64, b)
		}
	}
	out := make([][3][]float64, variants)
	for v := range variants {
		for m := range 3 {
			out[v][m] = make([]float64, b)
		}
	}
	return out, scenarioSamples
}
