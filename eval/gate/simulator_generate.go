package gate

import (
	"fmt"
)

type itemProbabilities struct {
	base        [3]float64
	conditional [][3][2]float64
	recall      []float64
}

func synthetic(cfg CalibrationConfig, c Cell, seed uint64) (Table, error) {
	rng := newSimulationRandom(seed, splitmix(seed))
	k := 1/c.Rho - 1
	out := make(Table, 0, len(cfg.Gate.Scenarios)*48*6)
	for _, s := range cfg.Gate.Scenarios {
		rates := baselineRates(cfg, s)
		p := [3]float64{rates.Success, 1 - rates.Unsupported, rates.Evidence}
		diff := [3]float64{c.DY, -c.DU, c.DE}
		var cond [3][2]float64
		for m := range 3 {
			a, b, err := conditional(p[m], diff[m], c.Delta)
			if err != nil {
				return nil, err
			}
			cond[m] = [2]float64{b, a}
		}
		for item := range 48 {
			theta := drawItem(rng, p, cond, c, k, len(cfg.Gate.Variants))
			for rep := 1; rep <= 2; rep++ {
				out = append(out, drawRepeat(rng, cfg, c, s, item, rep, theta)...)
			}
		}
	}
	return out, nil
}
func drawItem(r *simulationRandom, p [3]float64, cond [3][2]float64, c Cell, k float64, n int) itemProbabilities {
	out := itemProbabilities{conditional: make([][3][2]float64, n), recall: make([]float64, n)}
	for m := range 3 {
		out.base[m] = beta(r, p[m], k)
	}
	for v := range n {
		out.recall[v] = beta(r, c.Recall, k)
		for m := range 3 {
			for b := range 2 {
				out.conditional[v][m][b] = beta(r, cond[m][b], k)
			}
		}
	}
	return out
}
func drawRepeat(r *simulationRandom, cfg CalibrationConfig, c Cell, s string, item, rep int, p itemProbabilities) Table {
	u := copula(r, c.Kappa)
	var base [3]bool
	for m := range 3 {
		base[m] = u[m] < p.base[m]
	}
	id := fmt.Sprintf("%03d", item)
	out := make(Table, 0, 1+len(cfg.Gate.Variants))
	out = append(out, Row{Key: Key{cfg.Gate.Baseline, s, id, rep}, Success: base[0], Unsupported: !base[1], Evidence: base[2]})
	for v, name := range cfg.Gate.Variants {
		u = copula(r, c.Kappa)
		var good [3]bool
		for m := range 3 {
			b := 0
			if base[m] {
				b = 1
			}
			good[m] = u[m] < p.conditional[v][m][b]
		}
		// Qualification is the validated sampling fraction, independent of quality.
		q := r.Float64() < cfg.Qualifying[name][s]
		out = append(out, Row{Key: Key{name, s, id, rep}, Success: good[0], Unsupported: !good[1], Evidence: good[2], Qualifying: q, Recalled: q && u[4] < p.recall[v]})
	}
	return out
}
func baselineRates(cfg CalibrationConfig, s string) Rates {
	if cfg.Pilot == nil {
		return Rates{.85, .05, .90}
	}
	r := cfg.Pilot[s]
	return Rates{max(.02, min(.98, r.Success)), max(.02, min(.98, r.Unsupported)), max(.02, min(.98, r.Evidence))}
}
