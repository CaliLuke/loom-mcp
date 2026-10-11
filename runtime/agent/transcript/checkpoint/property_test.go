package checkpoint

// testGenerator is a fixed-seed Park–Miller generator for reproducible records.
// Its positive state is below 2^31; multiplication fits the repo's 64-bit int.
type testGenerator struct {
	state int
}

// Intn returns a reproducible value in [0, n); n must be positive.
func (g *testGenerator) Intn(n int) int {
	g.state = (g.state * 16807) % 2147483647
	return g.state % n
}
