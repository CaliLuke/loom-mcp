// Package opik explicitly exports stored experiments to Opik. It never uploads
// during evaluation and imports no provider SDK.
package opik

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

type (
	// Config selects the remote target and the exact application sharing policy.
	// Endpoint is the REST base URL ending in /api for a normal Opik deployment.
	Config struct {
		Endpoint   string
		Project    string
		Workspace  string
		APIKey     string
		HTTPClient *http.Client
		Policy     runrecord.ProjectionPolicy
	}
	// Client owns one explicit exporter configuration.
	Client struct{ config Config }
	// Result reports attempt mappings and explicit missing/incomplete evidence.
	// A non-nil error from Export or ExportStored means publication did not finish.
	Result struct {
		ExperimentID string          `json:"experiment_id"`
		Attempts     []AttemptResult `json:"attempts"`
		Incomplete   bool            `json:"incomplete"`
		Warnings     []string        `json:"warnings"`
	}
	// AttemptResult retains the stable mapping from local to remote evidence.
	AttemptResult struct {
		Key        eval.AttemptKey `json:"key"`
		TraceID    string          `json:"trace_id"`
		ItemID     string          `json:"item_id"`
		Incomplete bool            `json:"incomplete"`
		Warnings   []string        `json:"warnings"`
	}
)

const (
	fieldIncomplete = "incomplete"
	fieldWarnings   = "warnings"
	fieldProject    = "project_name"
	fieldDataset    = "dataset_name"
	fieldScenario   = "scenario"
	fieldExperiment = "source_experiment"
	fieldMetadata   = "metadata"
	fieldPolicy     = "policy"
	fieldItem       = "item"
	sourceSDK       = "sdk"
	statusRunning   = "running"
)

// New validates the explicit target and named application content policy.
func New(config Config) (*Client, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.User != nil || config.Project == "" || config.Policy.Name == "" || config.Policy.Content == nil {
		return nil, fmt.Errorf("opik: valid endpoint, project and named projection policy required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	return &Client{config: config}, nil
}
