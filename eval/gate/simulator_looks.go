package gate

import "strconv"

func simulateLooks(table Table, cfg Config, out *CellReport, eval evaluator) error {
	standalone := map[string]Status{}
	stopped := false
	for l, n := range []int{12, 24, 48} {
		rows, err := syntheticPrefix(table, n)
		if err != nil {
			return err
		}
		e, err := eval(rows, cfg, l+1)
		if err != nil {
			return err
		}
		if err := advanceStandalone(e, standalone); err != nil {
			return err
		}
		if !stopped {
			d, err := Decide(e, nil)
			if err != nil {
				return err
			}
			if d.Status != Continue {
				recordStop(out, e, d, n, len(cfg.Scenarios))
				stopped = true
			}
		}
		if stopped && standaloneStopped(standalone) {
			break
		}
	}
	for v, status := range standalone {
		if status == Pass {
			out.VariantPass[v]++
		}
	}
	return nil
}
func syntheticPrefix(table Table, n int) (Table, error) {
	rows := make(Table, 0, len(table))
	for _, r := range table {
		id, err := strconv.Atoi(r.Item)
		if err != nil {
			return nil, err
		}
		if id < n {
			rows = append(rows, r)
		}
	}
	return rows, nil
}
func advanceStandalone(e Evaluation, status map[string]Status) error {
	for _, v := range e.Variants {
		if status[v.Variant] == Pass || status[v.Variant] == Fail {
			continue
		}
		one := e
		one.Variants = []VariantResult{v, v}
		one.GuardFailures = nil
		for s, mean := range v.Scenarios {
			if mean < -.10 {
				one.GuardFailures = append(one.GuardFailures, s)
			}
		}
		d, err := Decide(one, nil)
		if err != nil {
			return err
		}
		status[v.Variant] = d.Status
	}
	return nil
}
func standaloneStopped(status map[string]Status) bool {
	for _, s := range status {
		if s == Continue {
			return false
		}
	}
	return true
}
func recordStop(out *CellReport, e Evaluation, d Decision, n, scenarios int) {
	l := e.Look - 1
	switch d.Status {
	case Pass:
		out.Pass++
		out.LookPass[l]++
		for j := l; j < 2; j++ {
			out.TruncatedPass[j]++
		}
	case Fail:
		out.LookFail[l]++
	case NotPassed:
		out.NotPassed++
	case Continue, Incomplete, Inconclusive:
	}
	out.ExpectedLooks += float64(e.Look)
	out.ExpectedItems += float64(n * scenarios)
	for _, v := range e.Variants {
		for _, c := range criteria(v) {
			key := v.Variant + "/" + c.name
			if out.Final[key] == nil {
				out.Final[key] = map[Status]float64{}
			}
			out.Final[key][c.result.Status]++
		}
	}
}
