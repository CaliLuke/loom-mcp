package discoverybench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"testing"

	assistantapi "example.com/assistant"
	assistant "example.com/assistant/gen/assistant"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type hostObservation struct {
	Name         string         `json:"name"`
	Accepted     bool           `json:"accepted"`
	ServiceCalls int            `json:"service_calls"`
	Error        string         `json:"error,omitempty"`
	Result       jsontext.Value `json:"result,omitempty"`
	Discovery    jsontext.Value `json:"discovery,omitempty"`
}

type countedService struct {
	assistant.Service
	calls int
}

func TestFrozenCorpus(t *testing.T) {
	for _, tc := range []struct{ path, digest string }{
		{"testdata/catalog.json", "5533903d33ecbb42f6523b353ba9bfb8c60c0a705ee499c9b28223736a66a94b"},
		{"testdata/cases.json", "a248254ef490c931f2284648a1c97f5a796b6b368f752c1081247ccbd020a62b"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			require.NoError(t, err)
			sum := sha256.Sum256(data)
			assert.Equal(t, tc.digest, hex.EncodeToString(sum[:]))
		})
	}
}

func TestGeneratedCatalogExactExport(t *testing.T) {
	want, err := os.ReadFile("testdata/catalog.json")
	require.NoError(t, err)
	got, err := exportCatalog(t.Context())
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

func TestGeneratedHostBoundary(t *testing.T) {
	hostBoundary(t)
}

func hostBoundary(t *testing.T) []hostObservation {
	t.Helper()
	observations := make([]hostObservation, 0, 5)
	for _, tc := range []struct {
		name, tool, payload string
		success             bool
	}{
		{"legal", "call_tool", `{"name":"analyze_sentiment","arguments":{"text":"great"}}`, true},
		{"hidden", "analyze_sentiment", `{"text":"great"}`, false},
		{"unknown", "call_tool", `{"name":"send_invoice","arguments":{}}`, false},
		{"required", "call_tool", `{"name":"summarize_text","arguments":{}}`, false},
		{"enum", "call_tool", `{"name":"execute_code","arguments":{"code":"print(1)","language":"rust"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &countedService{Service: assistantapi.NewAssistant()}
			registration, err := registrationFor(service)
			require.NoError(t, err)
			row := hostObservation{Name: tc.name}
			if tc.success {
				discovery, searchErr := invoke(t.Context(), registration, "search_tools", []byte(`{"query":"analyze_sentiment","max_results":1,"include_schemas":true}`))
				require.NoError(t, searchErr)
				require.Nil(t, discovery.Error)
				row.Discovery, searchErr = resultJSON(discovery)
				require.NoError(t, searchErr)
				assert.Contains(t, string(row.Discovery), `"name":"analyze_sentiment"`)
			}
			result, err := invoke(t.Context(), registration, tc.tool, []byte(tc.payload))
			require.NoError(t, err)
			require.NotNil(t, result)
			if tc.success {
				assert.Nil(t, result.Error)
				assert.Equal(t, 1, service.calls)
			} else {
				assert.NotNil(t, result.Error)
				assert.Zero(t, service.calls)
			}
			row.Accepted = result.Error == nil
			row.ServiceCalls = service.calls
			if result.Error != nil {
				row.Error = fmt.Sprint(result.Error)
			}
			row.Result, err = resultJSON(result)
			require.NoError(t, err)
			observations = append(observations, row)
		})
	}
	return observations
}

func (s *countedService) AnalyzeSentiment(ctx context.Context, p *assistant.AnalyzeSentimentPayload) (*assistant.AnalyzeSentimentResult, error) {
	s.calls++
	return s.Service.AnalyzeSentiment(ctx, p)
}

func (s *countedService) SummarizeText(ctx context.Context, p *assistant.SummarizeTextPayload) (*assistant.SummarizeTextResult, error) {
	s.calls++
	return s.Service.SummarizeText(ctx, p)
}

func (s *countedService) ExecuteCode(ctx context.Context, p *assistant.ExecuteCodePayload) (*assistant.ExecuteCodeResult, error) {
	s.calls++
	return s.Service.ExecuteCode(ctx, p)
}

// resultJSON preserves raw generated schema numbers through json/v2.
func resultJSON(result *planner.ToolResult) ([]byte, error) {
	return json.Marshal(result.Result, json.Deterministic(true))
}
