package gate

import (
	"fmt"
	"math"
	"math/rand/v2"
)

// simulationRandom uses PCG for reproducible statistics, never for secrets.
// Rejection sampling keeps bounded draws unbiased; Box-Muller supplies normals.
type simulationRandom struct{ source *rand.PCG }

func newSimulationRandom(a, b uint64) *simulationRandom {
	return &simulationRandom{source: rand.NewPCG(a, b)}
}

// conditional preserves the requested marginal difference while capping
// discordance at the largest feasible two-by-two joint distribution.
func conditional(p, d, delta float64) (float64, float64, error) {
	if p <= 0 || p >= 1 || p+d < 0 || p+d > 1 || delta < math.Abs(d) {
		return 0, 0, fmt.Errorf("infeasible marginal difference %g at baseline %g", d, p)
	}
	effective := min(delta, 2*p+d, 2*(1-p)-d)
	c10 := (effective - d) / 2
	c01 := (effective + d) / 2
	return max(0, min(1, (p-c10)/p)), max(0, min(1, c01/(1-p))), nil
}
func splitmix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
func gamma(r *simulationRandom, shape float64) float64 {
	if shape < 1 {
		return gamma(r, shape+1) * math.Pow(r.Float64(), 1/shape)
	}
	d := shape - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := r.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := r.Float64()
		if u < 1-.0331*x*x*x*x || math.Log(u) < .5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}
func beta(r *simulationRandom, p, k float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	a := gamma(r, p*k)
	b := gamma(r, (1-p)*k)
	return a / (a + b)
}
func copula(r *simulationRandom, kappa float64) [5]float64 {
	var out [5]float64
	z := math.Sqrt(kappa) * r.NormFloat64()
	for i := range out {
		out[i] = .5 * (1 + math.Erf((z+math.Sqrt(1-kappa)*r.NormFloat64())/math.Sqrt2))
	}
	return out
}

func (r *simulationRandom) Float64() float64 {
	return float64(r.source.Uint64()>>11) * (1.0 / (1 << 53))
}
func (r *simulationRandom) NormFloat64() float64 {
	return math.Sqrt(-2*math.Log(1-r.Float64())) * math.Cos(2*math.Pi*r.Float64())
}
func (r *simulationRandom) IntN(n int) int {
	// n is a slice length and therefore positive and representable as uint64.
	if n <= 0 {
		panic("gate: nonpositive random bound")
	}
	bound := uint64(n)
	threshold := -bound % bound
	for {
		v := r.source.Uint64()
		if v >= threshold {
			value := v % bound
			if value > uint64(math.MaxInt) {
				panic("gate: random index overflow")
			}
			return int(value)
		}
	}
}
