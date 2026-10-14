package gate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalibrationTargetsGuardDominates(t *testing.T) {
	cells := make([]CellReport, 0, 5)
	cells = append(cells, CellReport{Cell: Cell{Recall: .9}, Pass: .01, LookFail: [3]float64{.95}, Final: map[string]map[Status]float64{"hybrid/unsupported": {Pass: .01, Inconclusive: .99}}})
	for _, c := range []Cell{{DY: -.05, Recall: .9}, {DU: .02, Recall: .9}, {DE: -.05, Recall: .9}, {Recall: .8}} {
		cells = append(cells, CellReport{Cell: c, Pass: .02})
	}
	got := strings.Join(LegacyCalibrationTargets(cells), "\n")
	for _, line := range []string{"null PASS >= 0.8000: measured 0.0100", "success margin PASS <= 0.0500: measured 0.0200", "unsupported margin PASS <= 0.0500: measured 0.0200", "evidence margin PASS <= 0.0500: measured 0.0200", "recall margin PASS <= 0.0500: measured 0.0200", "look-1 FAIL range 0.9500..0.9500", "guard look-1 FAIL lower-bound range 0.9500..0.9500", "limiting factor: guard/look-1 FAIL"} {
		assert.Contains(t, got, line)
	}
}

func TestCalibrationTargetsDoesNotInventGuardCause(t *testing.T) {
	cells := []CellReport{{Cell: Cell{Recall: .9}, Pass: .01, LookFail: [3]float64{.95}, Final: map[string]map[Status]float64{"hybrid/success": {Fail: .95}}}}
	got := strings.Join(LegacyCalibrationTargets(cells), "\n")
	assert.Contains(t, got, "guard look-1 FAIL lower-bound range 0.0000..0.0000")
	assert.NotContains(t, got, "limiting factor: guard")
	assert.Contains(t, got, "success margin PASS <= 0.0500: not measured")
}
