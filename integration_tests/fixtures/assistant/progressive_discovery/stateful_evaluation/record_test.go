package statefulevaluation

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type timingResult struct {
	Arm             string
	Operation       string
	Warmup          invocation
	Samples         []invocation
	ExecuteMedianNS int64
	ExecuteP95NS    int64
	SetupMedianNS   int64
	SetupP95NS      int64
}
type evaluationRecord struct {
	Version                          int
	RecordedAt                       string
	GoVersion                        string
	OS                               string
	Architecture                     string
	GOMAXPROCS                       int
	CatalogSHA256                    string
	CasesSHA256                      string
	ConfigSHA256                     string
	TracesSHA256                     string
	Quality                          []qualityResult
	Turns                            []turnResult
	Pressure                         []pressureResult
	Timing                           []timingResult
	RuntimeActivitySuccesses         int
	RuntimeMaterializationLimitation string
}

// TestRecordEvaluation is opt-in because latency recording needs a quiet host.
func TestRecordEvaluation(t *testing.T) {
	if os.Getenv("LOOM_RECORD_STATEFUL_DISCOVERY") != "1" {
		t.Skip("set LOOM_RECORD_STATEFUL_DISCOVERY=1 only in a coordinated quiet timing window")
	}
	record := evaluationRecord{Version: 1, RecordedAt: time.Now().UTC().Format(time.RFC3339), GoVersion: runtime.Version(), OS: runtime.GOOS, Architecture: runtime.GOARCH, GOMAXPROCS: runtime.GOMAXPROCS(0), CatalogSHA256: catalogHash, CasesSHA256: casesHash, ConfigSHA256: configHash, TracesSHA256: tracesHash, RuntimeMaterializationLimitation: "generated local JSONValue result rejected after host effect; see TestGeneratedLocalRuntimeMaterializationLimitation"}
	record.Quality = runQuality(t)
	record.Turns = runTraces(t)
	record.Pressure = runPressure(t)
	record.Timing = sampleLatency(t)
	data, err := json.Marshal(record, json.Deterministic(true), jsontext.WithIndent("  "))
	require.NoError(t, err)
	root, err := os.OpenRoot("testdata")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, root.Close())
	}()
	require.NoError(t, root.WriteFile("results.json", append(data, '\n'), 0o600))
}

func TestRecordedEvaluationProvenance(t *testing.T) {
	root, err := os.OpenRoot("testdata")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, root.Close())
	}()
	data, err := root.ReadFile("results.json")
	require.NoError(t, err, "record the evaluation before freezing this artifact")
	var record evaluationRecord
	require.NoError(t, json.Unmarshal(data, &record))
	assert.Equal(t, catalogHash, record.CatalogSHA256)
	assert.Equal(t, casesHash, record.CasesSHA256)
	assert.Equal(t, configHash, record.ConfigSHA256)
	assert.Equal(t, tracesHash, record.TracesSHA256)
	assert.Zero(t, record.RuntimeActivitySuccesses)
	require.Len(t, record.Quality, 24)
	require.Len(t, record.Pressure, 12)
	require.Len(t, record.Timing, 4)
	for _, timing := range record.Timing {
		require.Len(t, timing.Samples, 100)
		for _, sample := range timing.Samples {
			assert.Equal(t, "generated_executor", sample.Stage)
			assert.Empty(t, sample.Failure)
		}
	}
}

// sampleLatency retains every payload, including existing unordered-search variants.
// JSON reporting and fresh runtime registration are excluded from ExecuteNS.
func sampleLatency(t *testing.T) []timingResult {
	t.Helper()
	rows := make([]timingResult, 0, 4)
	for _, arm := range []string{"compact", "native"} {
		f := newEvaluationFixture(t)
		key := discoveryKey{Agent: "a", Session: "latency"}
		require.Contains(t, f.discover(t, key, "summarize_text").Names, "summarize_text")
		for _, operation := range []string{"search_tools", "summarize_text"} {
			args := map[string]any{"text": "Alpha beta"}
			if operation == "search_tools" {
				args = map[string]any{"query": "lookup", "max_results": 5, "include_schemas": true}
			}
			row := timingResult{Arm: arm, Operation: operation, Warmup: f.invoke(t, arm, key, operation, args)}
			require.Empty(t, row.Warmup.Failure)
			execution := make([]int64, 0, 100)
			setup := make([]int64, 0, 100)
			for range 100 {
				sample := f.invoke(t, arm, key, operation, args)
				require.Empty(t, sample.Failure)
				row.Samples = append(row.Samples, sample)
				execution = append(execution, sample.ExecuteNS)
				setup = append(setup, sample.SetupNS)
			}
			slices.Sort(execution)
			slices.Sort(setup)
			row.ExecuteMedianNS, row.ExecuteP95NS = execution[49]+(execution[50]-execution[49])/2, execution[94]
			row.SetupMedianNS, row.SetupP95NS = setup[49]+(setup[50]-setup[49])/2, setup[94]
			rows = append(rows, row)
		}
	}
	return rows
}
