package discoverybench

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"runtime"
	"testing"
	"time"

	assistantapi "example.com/assistant"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/require"
)

type benchmarkCorpus struct {
	KValues []int           `json:"k_values"`
	Cases   []benchmarkCase `json:"cases"`
}

type benchmarkCase struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

type benchmarkConfig struct {
	Warmups       int            `json:"warmups"`
	Samples       int            `json:"samples"`
	MetadataCheck jsontext.Value `json:"metadata_check"`
}

type retrievalVariant struct {
	Payload            jsontext.Value `json:"payload"`
	NativePayloadBytes int            `json:"native_payload_bytes"`
}

type observation struct {
	CaseID         string             `json:"case_id"`
	K              int                `json:"k"`
	Variants       []retrievalVariant `json:"variants"`
	SampleVariants []int              `json:"sample_variants"`
	WarmupVariants []int              `json:"warmup_variants"`
	LatencyNS      []int64            `json:"latency_ns"`
}

type baselineRecording struct {
	HostBoundary  []hostObservation `json:"host_boundary"`
	Arm           string            `json:"arm"`
	GoVersion     string            `json:"go_version"`
	GOOS          string            `json:"goos"`
	GOARCH        string            `json:"goarch"`
	SetupNS       int64             `json:"setup_ns"`
	Results       []observation     `json:"results"`
	MetadataCheck jsontext.Value    `json:"metadata_check"`
}

// TestRecordBenchmark is an explicit observation command, never a timing gate.
func TestRecordBenchmark(t *testing.T) {
	record := os.Getenv("LOOM_DISCOVERY_BENCH_RECORD")
	if record == "" {
		t.Skip("set LOOM_DISCOVERY_BENCH_RECORD=1 to explicitly refresh observations")
	}
	require.Equal(t, "1", record)
	TestFrozenCorpus(t)
	TestGeneratedCatalogExactExport(t)
	var corpus benchmarkCorpus
	readJSON(t, "testdata/cases.json", &corpus)
	var config benchmarkConfig
	readJSON(t, "config.json", &config)
	require.Equal(t, 20, config.Warmups)
	require.Equal(t, 100, config.Samples)
	started := time.Now()
	registration, err := registrationFor(assistantapi.NewAssistant())
	require.NoError(t, err)
	recording := baselineRecording{Arm: "baseline", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, SetupNS: time.Since(started).Nanoseconds()}
	for _, tc := range corpus.Cases {
		for _, k := range corpus.KValues {
			recording.Results = append(recording.Results, observeSearch(t, registration, tc, k, config))
		}
	}
	metadata, err := invoke(t.Context(), registration, "search_tools", config.MetadataCheck)
	require.NoError(t, err)
	require.Nil(t, metadata.Error)
	recording.MetadataCheck, err = resultJSON(metadata)
	require.NoError(t, err)
	recording.HostBoundary = hostBoundary(t)
	require.NoError(t, os.MkdirAll("results", 0750))
	writeJSON(t, "results/baseline.json", recording)
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	root, err := os.OpenRoot(".")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, root.Close())
	})
	data, err := root.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, target))
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value, json.Deterministic(true))
	require.NoError(t, err)
	root, err := os.OpenRoot(".")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, root.Close())
	})
	require.NoError(t, root.WriteFile(path, data, 0600))
}

func observeSearch(t *testing.T, registration agentsruntime.ToolsetRegistration, tc benchmarkCase, k int, config benchmarkConfig) observation {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"query": tc.Query, "max_results": k, "include_schemas": true})
	require.NoError(t, err)
	result := observation{CaseID: tc.ID, K: k, LatencyNS: make([]int64, 0, config.Samples)}
	variantIndexes := make(map[string]int)
	for sample := range config.Warmups + config.Samples {
		started := time.Now()
		response, callErr := invoke(t.Context(), registration, "search_tools", payload)
		elapsed := time.Since(started).Nanoseconds()
		require.NoError(t, callErr)
		require.Nil(t, response.Error)
		raw, marshalErr := resultJSON(response)
		require.NoError(t, marshalErr)
		index, found := variantIndexes[string(raw)]
		if !found {
			index = len(result.Variants)
			variantIndexes[string(raw)] = index
			result.Variants = append(result.Variants, retrievalVariant{Payload: raw, NativePayloadBytes: len(raw)})
		}
		if sample >= config.Warmups {
			result.LatencyNS = append(result.LatencyNS, elapsed)
			result.SampleVariants = append(result.SampleVariants, index)
		} else {
			result.WarmupVariants = append(result.WarmupVariants, index)
		}
	}
	return result
}
