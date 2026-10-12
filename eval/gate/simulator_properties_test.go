package gate

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSyntheticMarginalsDiscordanceAndICC(t *testing.T) {
	cfg := CalibrationConfig{Gate: testConfig(), Qualifying: map[string]map[string]float64{"receipt_only": {"a": .6, "b": .6}, "hybrid": {"a": .6, "b": .6}}}
	c := Cell{DY: -.03, DU: .01, DE: -.03, Delta: .2, Rho: .5, Recall: .9, Kappa: .5}
	var base, variant, unsupported, evidence, discordant, q, recall, total, products, items float64
	var seed uint64
	for range 400 {
		rows, err := synthetic(cfg, c, seed)
		require.NoError(t, err)
		seed++
		for i := 0; i < len(rows); i += 3 {
			b, v := rows[i], rows[i+1]
			base += binary(b.Success)
			variant += binary(v.Success)
			unsupported += binary(v.Unsupported)
			evidence += binary(v.Evidence)
			discordant += binary(b.Success != v.Success)
			q += binary(v.Qualifying)
			recall += binary(v.Recalled)
			total++
			if b.Repeat == 1 {
				products += binary(b.Success && rows[i+3].Success)
				items++
			}
		}
	}
	p := base / total
	assert.InDelta(t, .85, p, .01)
	assert.InDelta(t, .82, variant/total, .01)
	assert.InDelta(t, .06, unsupported/total, .01)
	assert.InDelta(t, .87, evidence/total, .01)
	assert.InDelta(t, .2, discordant/total, .01)
	assert.InDelta(t, .6, q/total, .01)
	assert.InDelta(t, .9, recall/q, .01)
	assert.InDelta(t, .5, (products/items-p*p)/(p*(1-p)), .04)
}
